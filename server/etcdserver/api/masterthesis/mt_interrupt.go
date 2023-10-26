package masterthesis

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/pkg/v3/osutil"
	"go.etcd.io/etcd/server/v3/daproto"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const configPath = "/thesis/config/fault-config.yml"

var DaLogger *Logger
var sendConn *net.TCPConn
var jsonEncoder *json.Encoder
var jsonDecoder *json.Decoder
var recvConn *net.UnixConn
var respBytes []byte
var DaActionPicker = atomic.Pointer[ActionPicker]{}

var daEnabled bool

func init() {
	DaLogger = NewLogger("[THESIS]")

	var disableDa = os.Getenv("DA_DISABLE_INTERRUPT")
	daEnabled = disableDa != "1"
	if !daEnabled {
		DaLogger.Info("DETECTION DISABLED")
		return
	}

	DaLogger.Info("DETECTION ENABLED")
	//toDaSocketPath, exists := os.LookupEnv("TO_DA_CONTAINER_SOCKET_PATH")
	//if !exists {
	//	panic("To-DA Socket path env variable is not defined")
	//}

	//fromDaSocketPath, exists := os.LookupEnv("FROM_DA_CONTAINER_SOCKET_PATH")
	//if !exists {
	//	panic("From-DA Socket path env variable is not defined")
	//}

	toDaUnixAddr, err := net.ResolveTCPAddr("tcp", "da:8090")
	if err != nil {
		panic(fmt.Sprintf("Failed to resolve unix addr To-DA socket: '%s'", err.Error()))
	}

	//fromDaUnixAddr, err := net.ResolveUnixAddr("unixgram", fromDaSocketPath)
	//if err != nil {
	//	panic(fmt.Sprintf("Failed to resolve unix addr From-DA socket: '%s'", err.Error()))
	//}

	DaLogger.Info("Dialing DA unix domain socket on path '%s'", toDaUnixAddr.String())

	sendConn, err = net.DialTCP("tcp", nil, toDaUnixAddr)
	if err != nil {
		panic(fmt.Sprintf("Failed to dial DA unix socket: '%s'", err.Error()))
	}

	jsonEncoder = json.NewEncoder(sendConn)
	jsonDecoder = json.NewDecoder(sendConn)

	//recvConn, err = net.DialUnix("unixgram", nil, fromDaUnixAddr)
	//if err != nil {
	//	panic(fmt.Sprintf("Failed to resolve unix addr From-DA socket: '%s'", err.Error()))
	//}

	DaLogger.Info("Connection To-DA established on path '%s'!", toDaUnixAddr.String())
	//DaLogger.Printf("Connection From-DA established on path '%s'!\n", fromDaUnixAddr.String())

	respBytes = make([]byte, 10*4096)

	faultConfig, err := ReadFaultConfig(configPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to read fault config from path '%s': '%s'", configPath, err.Error()))
	}

	DaActionPicker.Store(NewActionPicker(faultConfig))

	httpServer := RunConfigApi()
	osutil.RegisterInterruptHandler(func() {
		httpCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(httpCtx)
	})

	//go func() {
	//	for {
	//		bytes := make([]byte, 10*4096)
	//		bytesRead, _, err := sendConn.ReadFromUnix(bytes)
	//		if err != nil {
	//			DaLogger.Printf("Failed to read message")
	//			continue
	//		}
	//
	//		bytes = bytes[:bytesRead]
	//		msg := daproto.Message{}
	//		err = proto.Unmarshal(bytes, &msg)
	//		if err != nil {
	//			DaLogger.Printf("Failed to unmarshal msg of length %d", bytesRead)
	//			continue
	//		}
	//
	//		DaLogger.Printf("Received message: ")
	//	}
	//}()
	raft.StateChangeCallbacks.Store("DaLogState", DaLogState)
}

var DaInterruptLock = sync.RWMutex{}
var DaSendLock = sync.Mutex{}
var DaDataLock = sync.Mutex{}
var lastIndex uint64 = 0
var lastTerm uint64 = 0
var lastLogTerm uint64 = 0

type kv struct {
	Key string
	Val string
}

func PickAction(message *raftpb.Message) {
	//DaLogger.Info("messageType %s with %d entries", message.Type, len(message.Entries))
	if !daEnabled {
		return
	}
	if message.Type == raftpb.MsgHeartbeat || message.Type == raftpb.MsgHeartbeatResp {
		return
	}

	var actionType daproto.ActionType
	if ForceActionType {
		actionType = ForcedActionType
	} else {
		action := DaActionPicker.Load().DetermineAction()
		actionType = action.Type()
	}

	DaLogger.Debug("Message %s has %d entries", message.Type.String(), len(message.Entries))
	DaLogger.Debug("Message: %s", message.String())
	hasForcedMessageKey := false
	for i := 0; i < len(message.Entries); i++ {
		kvData := message.Entries[i].Data
		if kvData == nil {
			DaLogger.Debug("Entry %d has no data", i)
			continue
		}

		req := etcdserverpb.InternalRaftRequest{}
		err := req.Unmarshal(kvData)
		if err != nil {
			DaLogger.ErrorErr(err, "Failed to unmarshal entry")
			continue
		}

		DaLogger.Debug("Got req: %s", req.String())
		if req.Put != nil {
			putKey := string(req.Put.Key)
			if putKey == ForcedMessageKey {
				hasForcedMessageKey = true
			}
			DaLogger.Debug("Got key: %s", putKey)
			putValue := string(req.Put.Value)
			DaLogger.Debug("Got value: %s", putValue)
		}
	}

	if (actionType == daproto.ActionType_NOOP_ACTION_TYPE) && (ForceOnMessageKey && hasForcedMessageKey || !ForceActionType) && message.Type == raftpb.MsgApp {
		DaDataLock.Lock()
		defer DaDataLock.Unlock()
		lastIndex = message.Index
		lastTerm = message.Term
		lastLogTerm = message.LogTerm
	} else if (actionType == daproto.ActionType_RESEND_LAST_MESSAGE_ACTION_TYPE) && (ForceOnMessageKey && hasForcedMessageKey || !ForceActionType) && message.Type == raftpb.MsgApp {
		tempIdx := message.Index
		tempTerm := message.Term
		tempLogTerm := message.LogTerm
		DaDataLock.Lock()
		defer DaDataLock.Unlock()
		message.Index = message.Index - 2
		//message.Term = message.Term + 1
		DaLogger.Info("Message %s has %d entries", message.Type.String(), len(message.Entries))
		DaLogger.Info("Message: %s", message.String())
		for i := 0; i < len(message.Entries); i++ {
			kvData := message.Entries[i].Data
			if kvData == nil {
				DaLogger.Info("Entry %d has no data", i)
				continue
			}
			req := etcdserverpb.InternalRaftRequest{}
			err := req.Unmarshal(kvData)
			if err != nil {
				DaLogger.ErrorErr(err, "Failed to unmarshal entry")
				continue
			}

			DaLogger.Debug("Got req: %s", req.String())
			if req.Put != nil {
				putKey := string(req.Put.Key)
				DaLogger.Info("Got key: %s", putKey)
				putValue := string(req.Put.Value)
				DaLogger.Info("Got value: %s", putValue)
			}

			DaLogger.Info("Old #%d entry %s", i, message.Entries[i].String())
			message.Entries[i].Index = message.Index + uint64(i) + 1
			DaLogger.Info("Tampered #%d entry %s", i, message.Entries[i].String())
			//message.Entries[i].Index = message.Entries[i].Index - 1
			//message.Entries[i].Term = message.Term
		}
		//message.Term = lastTerm
		//message.LogTerm = lastLogTerm
		lastIndex = tempIdx
		lastTerm = tempTerm
		lastLogTerm = tempLogTerm
		DaLogger.Info("Resending with index %d and term %d", message.Index, message.Term)
	} else {
		DaDataLock.Lock()
		lastIndex = message.Index
		lastTerm = message.Term
		lastLogTerm = message.LogTerm
		DaDataLock.Unlock()
		DaInterruptLock.RUnlock()
		//DaInterruptSendMsg(message, actionType)
		DaInterruptLock.RLock()
	}
}

func DaLogState(state string) {
	nodeState := daproto.NodeState_FOLLOWER
	if state == "PRE_CANDIDATE" || state == "CANDIDATE" {
		nodeState = daproto.NodeState_CANDIDATE
	} else if state == "LEADER" {
		nodeState = daproto.NodeState_LEADER
	}

	daMsg := daproto.Message{MessageType: daproto.MessageType_STATE_LOG, NodeState: nodeState}
	DaSendLock.Lock()
	err := jsonEncoder.Encode(&daMsg)
	DaSendLock.Unlock()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to marshal msg of type '%s'", daMsg.MessageType)
		return
	}
}

func DaInterruptSendMsg(message *raftpb.Message) {
	DaInterruptLock.Lock()
	defer DaInterruptLock.Unlock()

	logMsg := fmt.Sprintf("Sending message of type %s to %x", message.Type.String(), message.To)
	if len(message.Entries) != 0 {
		var entries []string
		for _, entry := range message.Entries {
			idx := entry.Index
			req := etcdserverpb.InternalRaftRequest{}

			reqType := "Unknown"
			err := req.Unmarshal(entry.Data)
			if err == nil {
				reqType = mapInternalRequest(&req)
			}

			entries = append(entries, fmt.Sprintf("(%s, Index: %d)", reqType, idx))
		}

		logMsg += fmt.Sprintf(" with %d entries: %s", len(message.Entries), strings.Join(entries, ", "))
	}

	logMsg += "."
	daMsg := daproto.Message{MessageType: daproto.MessageType_INTERRUPT, LogMessage: logMsg}
	DaSendLock.Lock()
	err := jsonEncoder.Encode(&daMsg)
	DaSendLock.Unlock()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to marshal msg of type '%s'", daMsg.MessageType)
		return
	}

	scanner := bufio.NewScanner(sendConn)
	scanner.Scan()
	err = scanner.Err()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to unmarshal response from DA")
		return
	}
}

func DaInterruptReceiveMsg(message *raftpb.Message) {
	DaInterruptLock.Lock()
	defer DaInterruptLock.Unlock()

	logMsg := fmt.Sprintf("Received message of type %s from %x", message.Type.String(), message.From)
	if len(message.Entries) != 0 {
		var entries []string
		for _, entry := range message.Entries {
			idx := entry.Index
			req := etcdserverpb.InternalRaftRequest{}

			reqType := "Unknown"
			err := req.Unmarshal(entry.Data)
			if err == nil {
				reqType = mapInternalRequest(&req)
			}

			entries = append(entries, fmt.Sprintf("(%s, Index: %d)", reqType, idx))
		}

		logMsg += fmt.Sprintf(" with %d entries: %s", len(message.Entries), strings.Join(entries, ", "))
	}

	logMsg += "."
	daMsg := daproto.Message{MessageType: daproto.MessageType_INTERRUPT, LogMessage: logMsg}
	DaSendLock.Lock()
	err := jsonEncoder.Encode(&daMsg)
	DaSendLock.Unlock()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to marshal msg of type '%s'", daMsg.MessageType)
		return
	}

	scanner := bufio.NewScanner(sendConn)
	scanner.Scan()
	err = scanner.Err()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to unmarshal response from DA")
		return
	}
}

func DaInterruptApply(r *etcdserverpb.InternalRaftRequest) {
	DaInterruptLock.Lock()
	defer DaInterruptLock.Unlock()

	logMsg := fmt.Sprintf("Applying entry %s", mapInternalRequest(r))

	logMsg += "."
	daMsg := daproto.Message{MessageType: daproto.MessageType_INTERRUPT, LogMessage: logMsg}
	DaSendLock.Lock()
	err := jsonEncoder.Encode(&daMsg)
	DaSendLock.Unlock()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to marshal msg of type '%s'", daMsg.MessageType)
		return
	}

	scanner := bufio.NewScanner(sendConn)
	scanner.Scan()
	err = scanner.Err()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to unmarshal response from DA")
		return
	}
}

func mapInternalRequest(request *etcdserverpb.InternalRaftRequest) string {
	if request.Range != nil {
		return fmt.Sprintf("Get on Key '%s'", request.Range.Key)
	}
	if request.Put != nil {
		return fmt.Sprintf("Put on Key '%s' and Value '%s'", request.Put.Key, request.Put.Value)
	}
	if request.DeleteRange != nil {
		return fmt.Sprintf("Delete on Key '%s'", request.DeleteRange.Key)
	}
	if request.Txn != nil {
		return "Txn"
	}
	if request.Compaction != nil {
		return "Compact"
	}
	if request.LeaseGrant != nil {
		return "LeaseGrant"
	}
	if request.LeaseRevoke != nil {
		return "LeaseRevoke"
	}
	if request.Alarm != nil {
		return "Alarm"
	}
	if request.LeaseCheckpoint != nil {
		return "LeaseCheckpoint"
	}
	if request.AuthEnable != nil {
		return "AuthEnable"
	}
	if request.AuthDisable != nil {
		return "AuthDisable"
	}
	if request.AuthStatus != nil {
		return "AuthStatus"
	}
	if request.Authenticate != nil {
		return "Authenticate"
	}
	if request.AuthUserAdd != nil {
		return "AuthUserAdd"
	}
	if request.AuthUserDelete != nil {
		return "AuthUserDelete"
	}
	if request.AuthUserGet != nil {
		return "AuthUserGet"
	}
	if request.AuthUserChangePassword != nil {
		return "AuthUserChangePassword"
	}
	if request.AuthUserGrantRole != nil {
		return "AuthUserGrantRole"
	}
	if request.AuthUserRevokeRole != nil {
		return "AuthUserRevokeRole"
	}
	if request.AuthUserList != nil {
		return "AuthUserList"
	}
	if request.AuthRoleList != nil {
		return "AuthRoleList"
	}
	if request.AuthRoleAdd != nil {
		return "AuthRoleAdd"
	}
	if request.AuthRoleDelete != nil {
		return "AuthRoleDelete"
	}
	if request.AuthRoleGet != nil {
		return "AuthRoleGet"
	}
	if request.AuthRoleGrantPermission != nil {
		return "AuthRoleGrantPermission"
	}
	if request.AuthRoleRevokePermission != nil {
		return "AuthRoleRevokePermission"
	}
	if request.ClusterVersionSet != nil {
		return "ClusterVersionSet"
	}
	if request.ClusterMemberAttrSet != nil {
		return "ClusterMemberAttrSet"
	}
	if request.DowngradeInfoSet != nil {
		return "DowngradeInfoSet"
	}

	return "Unknown"
}
