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

package lccc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/common/cauthdsl"
	"github.com/hyperledger/fabric/core/chaincode/shim"
	"github.com/hyperledger/fabric/core/common/ccprovider"
	"github.com/hyperledger/fabric/core/common/sysccprovider"
	"github.com/hyperledger/fabric/core/peer"
	"github.com/hyperledger/fabric/protos/common"
	pb "github.com/hyperledger/fabric/protos/peer"
	"github.com/hyperledger/fabric/protos/utils"
	"github.com/op/go-logging"
)

//The life cycle system chaincode manages chaincodes deployed
//on this peer. It manages chaincodes via Invoke proposals.
//     "Args":["deploy",<ChaincodeDeploymentSpec>]
//     "Args":["upgrade",<ChaincodeDeploymentSpec>]
//     "Args":["stop",<ChaincodeInvocationSpec>]
//     "Args":["start",<ChaincodeInvocationSpec>]

var logger = logging.MustGetLogger("lccc")

const (
	//CHAINCODETABLE prefix for chaincode tables
	CHAINCODETABLE = "chaincodes"

	//chaincode lifecyle commands

	//INSTALL install command
	INSTALL = "install"

	//DEPLOY deploy command
	DEPLOY = "deploy"

	//UPGRADE upgrade chaincode
	UPGRADE = "upgrade"

	//GETCCINFO get chaincode
	GETCCINFO = "getid"

	//GETDEPSPEC get ChaincodeDeploymentSpec
	GETDEPSPEC = "getdepspec"

	//GETCCDATA get ChaincodeData
	GETCCDATA = "getccdata"

	//GETCHAINCODES gets the instantiated chaincodes on a channel
	GETCHAINCODES = "getchaincodes"

	//GETINSTALLEDCHAINCODES gets the installed chaincodes on a peer
	GETINSTALLEDCHAINCODES = "getinstalledchaincodes"

	//characters used in chaincodenamespace
	specialChars = "/:[]${}"
)

//---------- the LCCC -----------------

// LifeCycleSysCC implements chaincode lifecycle and policies aroud it
type LifeCycleSysCC struct {
	// sccprovider is the interface with which we call
	// methods of the system chaincode package without
	// import cycles
	sccprovider sysccprovider.SystemChaincodeProvider
}

//----------------errors---------------

//AlreadyRegisteredErr Already registered error
type AlreadyRegisteredErr string

func (f AlreadyRegisteredErr) Error() string {
	return fmt.Sprintf("%s already registered", string(f))
}

//InvalidFunctionErr invalid function error
type InvalidFunctionErr string

func (f InvalidFunctionErr) Error() string {
	return fmt.Sprintf("invalid function to lccc %s", string(f))
}

//InvalidArgsLenErr invalid arguments length error
type InvalidArgsLenErr int

func (i InvalidArgsLenErr) Error() string {
	return fmt.Sprintf("invalid number of argument to lccc %d", int(i))
}

//InvalidArgsErr invalid arguments error
type InvalidArgsErr int

func (i InvalidArgsErr) Error() string {
	return fmt.Sprintf("invalid argument (%d) to lccc", int(i))
}

//TXExistsErr transaction exists error
type TXExistsErr string

func (t TXExistsErr) Error() string {
	return fmt.Sprintf("transaction exists %s", string(t))
}

//TXNotFoundErr transaction not found error
type TXNotFoundErr string

func (t TXNotFoundErr) Error() string {
	return fmt.Sprintf("transaction not found %s", string(t))
}

//InvalidDeploymentSpecErr invalide chaincode deployment spec error
type InvalidDeploymentSpecErr string

func (f InvalidDeploymentSpecErr) Error() string {
	return fmt.Sprintf("Invalid deployment spec : %s", string(f))
}

//ExistsErr chaincode exists error
type ExistsErr string

func (t ExistsErr) Error() string {
	return fmt.Sprintf("Chaincode exists %s", string(t))
}

//NotFoundErr chaincode not registered with LCCC error
type NotFoundErr string

func (t NotFoundErr) Error() string {
	return fmt.Sprintf("chaincode not found %s", string(t))
}

//InvalidChainNameErr invalid chain name error
type InvalidChainNameErr string

func (f InvalidChainNameErr) Error() string {
	return fmt.Sprintf("invalid chain name %s", string(f))
}

//InvalidChaincodeNameErr invalid chaincode name error
type InvalidChaincodeNameErr string

func (f InvalidChaincodeNameErr) Error() string {
	return fmt.Sprintf("invalid chain code name %s", string(f))
}

//MarshallErr error marshaling/unmarshalling
type MarshallErr string

func (m MarshallErr) Error() string {
	return fmt.Sprintf("error while marshalling %s", string(m))
}

//IdenticalVersionErr trying to upgrade to same version of Chaincode
type IdenticalVersionErr string

func (f IdenticalVersionErr) Error() string {
	return fmt.Sprintf("chain code with the same version exists %s", string(f))
}

//InvalidVersionErr trying to upgrade to same version of Chaincode
type InvalidVersionErr string

func (f InvalidVersionErr) Error() string {
	return fmt.Sprintf("invalid version %s", string(f))
}

//EmptyVersionErr trying to upgrade to same version of Chaincode
type EmptyVersionErr string

func (f EmptyVersionErr) Error() string {
	return fmt.Sprintf("version not provided for chaincode %s", string(f))
}

//-------------- helper functions ------------------
//create the chaincode on the given chain
func (lccc *LifeCycleSysCC) createChaincode(stub shim.ChaincodeStubInterface, chainname string, ccname string, version string, cccode []byte, policy []byte, escc []byte, vscc []byte) (*ccprovider.ChaincodeData, error) {
	return lccc.putChaincodeData(stub, chainname, ccname, version, cccode, policy, escc, vscc)
}

//upgrade the chaincode on the given chain
func (lccc *LifeCycleSysCC) upgradeChaincode(stub shim.ChaincodeStubInterface, chainname string, ccname string, version string, cccode []byte, policy []byte, escc []byte, vscc []byte) (*ccprovider.ChaincodeData, error) {
	return lccc.putChaincodeData(stub, chainname, ccname, version, cccode, policy, escc, vscc)
}

//create the chaincode on the given chain
func (lccc *LifeCycleSysCC) putChaincodeData(stub shim.ChaincodeStubInterface, chainname string, ccname string, version string, cccode []byte, policy []byte, escc []byte, vscc []byte) (*ccprovider.ChaincodeData, error) {
	// check that escc and vscc are real system chaincodes
	if !lccc.sccprovider.IsSysCC(string(escc)) {
		return nil, fmt.Errorf("%s is not a valid endorsement system chaincode", string(escc))
	}
	if !lccc.sccprovider.IsSysCC(string(vscc)) {
		return nil, fmt.Errorf("%s is not a valid validation system chaincode", string(vscc))
	}

	cd := &ccprovider.ChaincodeData{Name: ccname, Version: version, DepSpec: cccode, Policy: policy, Escc: string(escc), Vscc: string(vscc)}
	cdbytes, err := proto.Marshal(cd)
	if err != nil {
		return nil, err
	}

	if cdbytes == nil {
		return nil, MarshallErr(ccname)
	}

	err = stub.PutState(ccname, cdbytes)

	return cd, err
}

//checks for existence of chaincode on the given chain
func (lccc *LifeCycleSysCC) getChaincode(stub shim.ChaincodeStubInterface, ccname string, checkFS bool) (*ccprovider.ChaincodeData, []byte, error) {
	cdbytes, err := stub.GetState(ccname)
	if err != nil {
		return nil, nil, err
	}

	if cdbytes != nil {
		cd := &ccprovider.ChaincodeData{}
		err = proto.Unmarshal(cdbytes, cd)
		if err != nil {
			return nil, nil, MarshallErr(ccname)
		}

		if checkFS {
			cd.DepSpec, _, err = ccprovider.GetChaincodeFromFS(ccname, cd.Version)
			if err != nil {
				return cd, nil, InvalidDeploymentSpecErr(err.Error())
			}
		}

		return cd, cdbytes, nil
	}

	return nil, nil, NotFoundErr(ccname)
}

// getChaincodes returns all chaincodes instantiated on this LCCC's channel
func (lccc *LifeCycleSysCC) getChaincodes(stub shim.ChaincodeStubInterface) pb.Response {
	// get all rows from LCCC
	itr, err := stub.GetStateByRange("", "")

	if err != nil {
		return shim.Error(err.Error())
	}
	defer itr.Close()

	// array to store metadata for all chaincode entries from LCCC
	var ccInfoArray []*pb.ChaincodeInfo

	for itr.HasNext() {
		_, value, err := itr.Next()
		if err != nil {
			return shim.Error(err.Error())
		}

		ccdata := &ccprovider.ChaincodeData{}
		if err = proto.Unmarshal(value, ccdata); err != nil {
			return shim.Error(err.Error())
		}

		ccdepspec := &pb.ChaincodeDeploymentSpec{}
		if err = proto.Unmarshal(ccdata.DepSpec, ccdepspec); err != nil {
			return shim.Error(err.Error())
		}

		path := ccdepspec.GetChaincodeSpec().ChaincodeId.Path
		input := ccdepspec.GetChaincodeSpec().Input.String()

		ccInfo := &pb.ChaincodeInfo{Name: ccdata.Name, Version: ccdata.Version, Path: path, Input: input, Escc: ccdata.Escc, Vscc: ccdata.Vscc}

		// add this specific chaincode's metadata to the array of all chaincodes
		ccInfoArray = append(ccInfoArray, ccInfo)
	}
	// add array with info about all instantiated chaincodes to the query
	// response proto
	cqr := &pb.ChaincodeQueryResponse{Chaincodes: ccInfoArray}

	cqrbytes, err := proto.Marshal(cqr)
	if err != nil {
		return shim.Error(err.Error())
	}

	return shim.Success(cqrbytes)
}

// getInstalledChaincodes returns all chaincodes installed on the peer
func (lccc *LifeCycleSysCC) getInstalledChaincodes() pb.Response {
	// get chaincode query response proto which contains information about all
	// installed chaincodes
	cqr, err := ccprovider.GetInstalledChaincodes()
	if err != nil {
		return shim.Error(err.Error())
	}

	cqrbytes, err := proto.Marshal(cqr)
	if err != nil {
		return shim.Error(err.Error())
	}

	return shim.Success(cqrbytes)
}

//do access control
func (lccc *LifeCycleSysCC) acl(stub shim.ChaincodeStubInterface, chainname string, cds *pb.ChaincodeDeploymentSpec) error {
	return nil
}

//check validity of chain name
func (lccc *LifeCycleSysCC) isValidChainName(chainname string) bool {
	//TODO we probably need more checks
	if chainname == "" {
		return false
	}
	return true
}

//check validity of chaincode name
func (lccc *LifeCycleSysCC) isValidChaincodeName(chaincodename string) bool {
	//TODO we probably need more checks
	if chaincodename == "" {
		return false
	}

	//do not allow special characters in chaincode name
	if strings.ContainsAny(chaincodename, specialChars) {
		return false
	}

	return true
}

//this implements "install" Invoke transaction
func (lccc *LifeCycleSysCC) executeInstall(stub shim.ChaincodeStubInterface, depSpec []byte) error {
	cds, err := utils.GetChaincodeDeploymentSpec(depSpec)

	if err != nil {
		return err
	}

	if !lccc.isValidChaincodeName(cds.ChaincodeSpec.ChaincodeId.Name) {
		return InvalidChaincodeNameErr(cds.ChaincodeSpec.ChaincodeId.Name)
	}

	if cds.ChaincodeSpec.ChaincodeId.Version == "" {
		return EmptyVersionErr(cds.ChaincodeSpec.ChaincodeId.Name)
	}

	if err = ccprovider.PutChaincodeIntoFS(cds); err != nil {
		return fmt.Errorf("Error installing chaincode code %s:%s(%s)", cds.ChaincodeSpec.ChaincodeId.Name, cds.ChaincodeSpec.ChaincodeId.Version, err)
	}

	return err
}

//this implements "deploy" Invoke transaction
func (lccc *LifeCycleSysCC) executeDeploy(stub shim.ChaincodeStubInterface, chainname string, depSpec []byte, policy []byte, escc []byte, vscc []byte) error {
	cds, err := utils.GetChaincodeDeploymentSpec(depSpec)

	if err != nil {
		return err
	}

	if !lccc.isValidChaincodeName(cds.ChaincodeSpec.ChaincodeId.Name) {
		return InvalidChaincodeNameErr(cds.ChaincodeSpec.ChaincodeId.Name)
	}

	if err = lccc.acl(stub, chainname, cds); err != nil {
		return err
	}

	cd, _, err := lccc.getChaincode(stub, cds.ChaincodeSpec.ChaincodeId.Name, true)
	if cd != nil {
		return ExistsErr(cds.ChaincodeSpec.ChaincodeId.Name)
	}

	if cds.ChaincodeSpec.ChaincodeId.Version == "" {
		return EmptyVersionErr(cds.ChaincodeSpec.ChaincodeId.Name)
	}

	_, err = lccc.createChaincode(stub, chainname, cds.ChaincodeSpec.ChaincodeId.Name, cds.ChaincodeSpec.ChaincodeId.Version, depSpec, policy, escc, vscc)

	return err
}

func (lccc *LifeCycleSysCC) getUpgradeVersion(cd *ccprovider.ChaincodeData, cds *pb.ChaincodeDeploymentSpec) (string, error) {
	if cd.Version == cds.ChaincodeSpec.ChaincodeId.Version {
		return "", IdenticalVersionErr(cds.ChaincodeSpec.ChaincodeId.Name)
	}

	if cds.ChaincodeSpec.ChaincodeId.Version != "" {
		return cds.ChaincodeSpec.ChaincodeId.Version, nil
	}

	//user did not specifcy Version. the previous version better be a number
	v, err := strconv.ParseInt(cd.Version, 10, 32)

	//This should never happen as long we don't expose version as version is computed internally
	//so panic till we find a need to relax
	if err != nil {
		return "", InvalidVersionErr(cd.Version)
	}

	// replace the ChaincodeDeploymentSpec using the next version
	newVersion := fmt.Sprintf("%d", (v + 1))

	return newVersion, nil
}

//this implements "upgrade" Invoke transaction
func (lccc *LifeCycleSysCC) executeUpgrade(stub shim.ChaincodeStubInterface, chainName string, depSpec []byte, policy []byte, escc []byte, vscc []byte) ([]byte, error) {
	cds, err := utils.GetChaincodeDeploymentSpec(depSpec)
	if err != nil {
		return nil, err
	}

	if err = lccc.acl(stub, chainName, cds); err != nil {
		return nil, err
	}

	chaincodeName := cds.ChaincodeSpec.ChaincodeId.Name
	if !lccc.isValidChaincodeName(chaincodeName) {
		return nil, InvalidChaincodeNameErr(chaincodeName)
	}

	// check for existence of chaincode
	cd, _, err := lccc.getChaincode(stub, chaincodeName, true)
	if cd == nil {
		return nil, NotFoundErr(chainName)
	}

	ver, err := lccc.getUpgradeVersion(cd, cds)
	if err != nil {
		return nil, err
	}

	newCD, err := lccc.upgradeChaincode(stub, chainName, chaincodeName, ver, depSpec, policy, escc, vscc)
	if err != nil {
		return nil, err
	}

	return []byte(newCD.Version), nil
}

//-------------- the chaincode stub interface implementation ----------

//Init only initializes the system chaincode provider
func (lccc *LifeCycleSysCC) Init(stub shim.ChaincodeStubInterface) pb.Response {
	lccc.sccprovider = sysccprovider.GetSystemChaincodeProvider()
	return shim.Success(nil)
}

// getDefaultEndorsementPolicy returns the default
// endorsement policy for the specified chain; it
// is used in case the deployer has not specified a
// custom one
func (lccc *LifeCycleSysCC) getDefaultEndorsementPolicy(chain string) []byte {
	// we create an array of principals, one principal
	// per application MSP defined on this chain
	ids := peer.GetMSPIDs(chain)
	sort.Strings(ids)
	principals := make([]*common.MSPPrincipal, len(ids))
	sigspolicy := make([]*common.SignaturePolicy, len(ids))
	for i, id := range ids {
		principals[i] = &common.MSPPrincipal{
			PrincipalClassification: common.MSPPrincipal_ROLE,
			Principal:               utils.MarshalOrPanic(&common.MSPRole{Role: common.MSPRole_MEMBER, MspIdentifier: id})}
		sigspolicy[i] = cauthdsl.SignedBy(int32(i))
	}

	// create the policy: it requires exactly 1 signature from any of the principals
	p := &common.SignaturePolicyEnvelope{
		Version:    0,
		Policy:     cauthdsl.NOutOf(1, sigspolicy),
		Identities: principals,
	}

	return utils.MarshalOrPanic(p)
}

// Invoke implements lifecycle functions "deploy", "start", "stop", "upgrade".
// Deploy's arguments -  {[]byte("deploy"), []byte(<chainname>), <unmarshalled pb.ChaincodeDeploymentSpec>}
//
// Invoke also implements some query-like functions
// Get chaincode arguments -  {[]byte("getid"), []byte(<chainname>), []byte(<chaincodename>)}
func (lccc *LifeCycleSysCC) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	args := stub.GetArgs()
	if len(args) < 1 {
		return shim.Error(InvalidArgsLenErr(len(args)).Error())
	}

	function := string(args[0])

	switch function {
	case INSTALL:
		if len(args) < 2 {
			return shim.Error(InvalidArgsLenErr(len(args)).Error())
		}

		depSpec := args[1]

		err := lccc.executeInstall(stub, depSpec)
		if err != nil {
			return shim.Error(err.Error())
		}
		return shim.Success([]byte("OK"))
	case DEPLOY:
		if len(args) < 3 || len(args) > 6 {
			return shim.Error(InvalidArgsLenErr(len(args)).Error())
		}

		//chain the chaincode shoud be associated with. It
		//should be created with a register call
		chainname := string(args[1])

		if !lccc.isValidChainName(chainname) {
			return shim.Error(InvalidChainNameErr(chainname).Error())
		}

		depSpec := args[2]

		// optional arguments here (they can each be nil and may or may not be present)
		// args[3] is a marshalled SignaturePolicyEnvelope representing the endorsement policy
		// args[4] is the name of escc
		// args[5] is the name of vscc
		var policy []byte
		if len(args) > 3 && len(args[3]) > 0 {
			policy = args[3]
		} else {
			policy = lccc.getDefaultEndorsementPolicy(chainname)
		}

		var escc []byte
		if len(args) > 4 && args[4] != nil {
			escc = args[4]
		} else {
			escc = []byte("escc")
		}

		var vscc []byte
		if len(args) > 5 && args[5] != nil {
			vscc = args[5]
		} else {
			vscc = []byte("vscc")
		}

		err := lccc.executeDeploy(stub, chainname, depSpec, policy, escc, vscc)
		if err != nil {
			return shim.Error(err.Error())
		}
		return shim.Success(nil)
	case UPGRADE:
		if len(args) < 3 || len(args) > 6 {
			return shim.Error(InvalidArgsLenErr(len(args)).Error())
		}

		chainname := string(args[1])
		if !lccc.isValidChainName(chainname) {
			return shim.Error(InvalidChainNameErr(chainname).Error())
		}

		depSpec := args[2]

		// optional arguments here (they can each be nil and may or may not be present)
		// args[3] is a marshalled SignaturePolicyEnvelope representing the endorsement policy
		// args[4] is the name of escc
		// args[5] is the name of vscc
		var policy []byte
		if len(args) > 3 && len(args[3]) > 0 {
			policy = args[3]
		} else {
			policy = lccc.getDefaultEndorsementPolicy(chainname)
		}

		var escc []byte
		if len(args) > 4 && args[4] != nil {
			escc = args[4]
		} else {
			escc = []byte("escc")
		}

		var vscc []byte
		if len(args) > 5 && args[5] != nil {
			vscc = args[5]
		} else {
			vscc = []byte("vscc")
		}

		verBytes, err := lccc.executeUpgrade(stub, chainname, depSpec, policy, escc, vscc)
		if err != nil {
			return shim.Error(err.Error())
		}
		return shim.Success(verBytes)
	case GETCCINFO, GETDEPSPEC, GETCCDATA:
		if len(args) != 3 {
			return shim.Error(InvalidArgsLenErr(len(args)).Error())
		}

		chain := string(args[1])
		ccname := string(args[2])

		//check the FS only for deployment spec
		//other calls are looking for LCCC entries only
		checkFS := false
		if function == GETDEPSPEC {
			checkFS = true
		}
		cd, cdbytes, err := lccc.getChaincode(stub, ccname, checkFS)
		if cd == nil || cdbytes == nil {
			logger.Errorf("ChaincodeId: %s does not exist on channel: %s(err:%s)", ccname, chain, err)
			return shim.Error(TXNotFoundErr(ccname + "/" + chain).Error())
		}

		switch function {
		case GETCCINFO:
			return shim.Success([]byte(cd.Name))
		case GETCCDATA:
			return shim.Success(cdbytes)
		default:
			return shim.Success(cd.DepSpec)
		}
	case GETCHAINCODES:
		if len(args) != 1 {
			return shim.Error(InvalidArgsLenErr(len(args)).Error())
		}
		return lccc.getChaincodes(stub)
	case GETINSTALLEDCHAINCODES:
		if len(args) != 1 {
			return shim.Error(InvalidArgsLenErr(len(args)).Error())
		}
		return lccc.getInstalledChaincodes()
	}

	return shim.Error(InvalidFunctionErr(function).Error())
}
