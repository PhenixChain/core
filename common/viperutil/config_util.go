/*
Copyright IBM Corp. 2016 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

                 http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package viperutil

import (
	"fmt"
	"io/ioutil"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"encoding/json"
	"encoding/pem"

	"github.com/mitchellh/mapstructure"
	"github.com/op/go-logging"
	"github.com/spf13/viper"
)

var logger = logging.MustGetLogger("viperutil")

func getKeysRecursively(base string, v *viper.Viper, nodeKeys map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for key := range nodeKeys {
		fqKey := base + key
		val := v.Get(fqKey)
		if m, ok := val.(map[interface{}]interface{}); ok {
			logger.Debugf("Found map[interface{}]interface{} value for %s", fqKey)
			tmp := make(map[string]interface{})
			for ik, iv := range m {
				cik, ok := ik.(string)
				if !ok {
					panic("Non string key-entry")
				}
				tmp[cik] = iv
			}
			result[key] = getKeysRecursively(fqKey+".", v, tmp)
		} else if m, ok := val.(map[string]interface{}); ok {
			logger.Debugf("Found map[string]interface{} value for %s", fqKey)
			result[key] = getKeysRecursively(fqKey+".", v, m)
		} else if m, ok := unmarshalJSON(val); ok {
			logger.Debugf("Found real value for %s setting to map[string]string %v", fqKey, m)
			result[key] = m
		} else {
			if val == nil {
				fileSubKey := fqKey + ".File"
				fileVal := v.Get(fileSubKey)
				if fileVal != nil {
					result[key] = map[string]interface{}{"File": fileVal}
					continue
				}
			}
			logger.Debugf("Found real value for %s setting to %T %v", fqKey, val, val)
			result[key] = val

		}
	}
	return result
}

func unmarshalJSON(val interface{}) (map[string]string, bool) {
	mp := map[string]string{}
	s, ok := val.(string)
	if !ok {
		logger.Debugf("Unmarshal JSON: value is not a string: %v", val)
		return nil, false
	}
	err := json.Unmarshal([]byte(s), &mp)
	if err != nil {
		logger.Debugf("Unmarshal JSON: value cannot be unmarshalled: %s", err)
		return nil, false
	}
	return mp, true
}

// customDecodeHook adds the additional functions of parsing durations from strings
// as well as parsing strings of the format "[thing1, thing2, thing3]" into string slices
// Note that whitespace around slice elements is removed
func customDecodeHook() mapstructure.DecodeHookFunc {
	durationHook := mapstructure.StringToTimeDurationHookFunc()
	return func(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
		dur, err := mapstructure.DecodeHookExec(durationHook, f, t, data)
		if err == nil {
			if _, ok := dur.(time.Duration); ok {
				return dur, nil
			}
		}

		if f.Kind() != reflect.String {
			return data, nil
		}

		raw := data.(string)
		l := len(raw)
		if l > 1 && raw[0] == '[' && raw[l-1] == ']' {
			slice := strings.Split(raw[1:l-1], ",")
			for i, v := range slice {
				slice[i] = strings.TrimSpace(v)
			}
			return slice, nil
		}

		return data, nil
	}
}

func byteSizeDecodeHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		if f != reflect.String || t != reflect.Uint32 {
			return data, nil
		}
		raw := data.(string)
		if raw == "" {
			return data, nil
		}
		var re = regexp.MustCompile(`^(?P<size>[0-9]+)\s*(?i)(?P<unit>(k|m|g))b?$`)
		if re.MatchString(raw) {
			size, err := strconv.ParseUint(re.ReplaceAllString(raw, "${size}"), 0, 64)
			if err != nil {
				return data, nil
			}
			unit := re.ReplaceAllString(raw, "${unit}")
			switch strings.ToLower(unit) {
			case "g":
				size = size << 10
				fallthrough
			case "m":
				size = size << 10
				fallthrough
			case "k":
				size = size << 10
			}
			if size > math.MaxUint32 {
				return size, fmt.Errorf("value '%s' overflows uint32", raw)
			}
			return size, nil
		}
		return data, nil
	}
}

func stringFromFileDecodeHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		// "to" type should be string
		if t != reflect.String {
			return data, nil
		}
		// "from" type should be map
		if f != reflect.Map {
			return data, nil
		}
		v := reflect.ValueOf(data)
		switch v.Kind() {
		case reflect.String:
			return data, nil
		case reflect.Map:
			d := data.(map[string]interface{})
			fileName, ok := d["File"]
			if !ok {
				fileName, ok = d["file"]
			}
			switch {
			case ok && fileName != nil:
				bytes, err := ioutil.ReadFile(fileName.(string))
				if err != nil {
					return data, err
				}
				return string(bytes), nil
			case ok:
				// fileName was nil
				return nil, fmt.Errorf("Value of File: was nil")
			}
		}
		return data, nil
	}
}

func pemBlocksFromFileDecodeHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		// "to" type should be string
		if t != reflect.Slice {
			return data, nil
		}
		// "from" type should be map
		if f != reflect.Map {
			return data, nil
		}
		v := reflect.ValueOf(data)
		switch v.Kind() {
		case reflect.String:
			return data, nil
		case reflect.Map:
			var fileName string
			var ok bool
			switch d := data.(type) {
			case map[string]string:
				fileName, ok = d["File"]
				if !ok {
					fileName, ok = d["file"]
				}
			case map[string]interface{}:
				var fileI interface{}
				fileI, ok = d["File"]
				if !ok {
					fileI, ok = d["file"]
				}
				fileName, ok = fileI.(string)
			}

			switch {
			case ok && fileName != "":
				var result []string
				bytes, err := ioutil.ReadFile(fileName)
				if err != nil {
					return data, err
				}
				for len(bytes) > 0 {
					var block *pem.Block
					block, bytes = pem.Decode(bytes)
					if block == nil {
						break
					}
					if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
						continue
					}
					result = append(result, string(pem.EncodeToMemory(block)))
				}
				return result, nil
			case ok:
				// fileName was nil
				return nil, fmt.Errorf("Value of File: was nil")
			}
		}
		return data, nil
	}
}

// EnhancedExactUnmarshal is intended to unmarshal a config file into a structure
// producing error when extraneous variables are introduced and supporting
// the time.Duration type
func EnhancedExactUnmarshal(v *viper.Viper, output interface{}) error {
	baseKeys := v.AllSettings() // AllKeys doesn't actually return all keys, it only returns the base ones
	leafKeys := getKeysRecursively("", v, baseKeys)

	logger.Infof("%+v", leafKeys)
	config := &mapstructure.DecoderConfig{
		ErrorUnused:      true,
		Metadata:         nil,
		Result:           output,
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			customDecodeHook(),
			byteSizeDecodeHook(),
			stringFromFileDecodeHook(),
			pemBlocksFromFileDecodeHook(),
		),
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return err
	}
	return decoder.Decode(leafKeys)
}
