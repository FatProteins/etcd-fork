package rafthttp

import (
	"context"
	"fmt"
	"go.etcd.io/etcd/pkg/v3/osutil"
	"go.etcd.io/etcd/server/v3/daproto"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const configPath = "/thesis/config/fault-config.yml"

var DaLogger *Logger
var sendConn *net.UnixConn
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
	toDaSocketPath, exists := os.LookupEnv("TO_DA_CONTAINER_SOCKET_PATH")
	if !exists {
		panic("To-DA Socket path env variable is not defined")
	}

	//fromDaSocketPath, exists := os.LookupEnv("FROM_DA_CONTAINER_SOCKET_PATH")
	//if !exists {
	//	panic("From-DA Socket path env variable is not defined")
	//}

	toDaUnixAddr, err := net.ResolveUnixAddr("unix", toDaSocketPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to resolve unix addr To-DA socket: '%s'", err.Error()))
	}

	//fromDaUnixAddr, err := net.ResolveUnixAddr("unixgram", fromDaSocketPath)
	//if err != nil {
	//	panic(fmt.Sprintf("Failed to resolve unix addr From-DA socket: '%s'", err.Error()))
	//}

	DaLogger.Info("Dialing DA unix domain socket on path '%s'", toDaUnixAddr.String())

	sendConn, err = net.DialUnix("unix", nil, toDaUnixAddr)
	if err != nil {
		panic(fmt.Sprintf("Failed to dial DA unix socket: '%s'", err.Error()))
	}

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
}

var daInterruptLock = sync.RWMutex{}
var daDataLock = sync.Mutex{}
var lastIndex uint64 = 0
var lastTerm uint64 = 0
var lastLogTerm uint64 = 0

func pickAction(message *raftpb.Message) {
	if !daEnabled {
		return
	}
	if message.Type == raftpb.MsgHeartbeat || message.Type == raftpb.MsgHeartbeatResp {
		return
	}

	action := DaActionPicker.Load().DetermineAction()
	actionType := action.Type()
	if actionType == daproto.ActionType_NOOP_ACTION_TYPE {
		daDataLock.Lock()
		defer daDataLock.Unlock()
		lastIndex = message.Index
		lastTerm = message.Term
		lastLogTerm = message.LogTerm
	} else if actionType == daproto.ActionType_RESEND_LAST_MESSAGE_ACTION_TYPE {
		tempIdx := message.Index
		tempTerm := message.Term
		tempLogTerm := message.LogTerm
		daDataLock.Lock()
		defer daDataLock.Unlock()
		message.Index = lastIndex
		message.Term = lastTerm
		message.LogTerm = lastLogTerm
		lastIndex = tempIdx
		lastTerm = tempTerm
		lastLogTerm = tempLogTerm
	} else {
		daDataLock.Lock()
		lastIndex = message.Index
		lastTerm = message.Term
		lastLogTerm = message.LogTerm
		daDataLock.Unlock()
		daInterruptLock.RUnlock()
		daInterrupt(message, actionType)
		daInterruptLock.RLock()
	}
}

func daInterrupt(message *raftpb.Message, actionType daproto.ActionType) {
	daInterruptLock.Lock()
	defer daInterruptLock.Unlock()

	daMsg := daproto.Message{MessageType: mapMsgType(message.Type), ActionType: actionType}
	//DaLogger.Printf("Received interrupt for msg of type '%s'\n", daMsg.MessageType)
	daMsgBytes, err := proto.Marshal(&daMsg)
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to marshal msg of type '%s'", daMsg.MessageType)
		return
	}

	_, err = sendConn.Write(daMsgBytes)
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to write msg to DA socket")
		return
	}

	//DaLogger.Printf("Sent msg of type '%s'", daMsg.MessageType)

	bytesRead, err := sendConn.Read(respBytes)
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to read response from DA")
		return
	}

	respBytes = respBytes[:bytesRead]
	daResp := daproto.Message{}
	err = proto.Unmarshal(respBytes, &daResp)
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to unmarshal response from DA to protobuf msg")
		return
	}

	//DaLogger.Printf("Successfully received response from DA: %s\n", daResp.String())
}

func mapMsgType(msgType raftpb.MessageType) daproto.MessageType {
	switch msgType {
	case raftpb.MsgHeartbeat, raftpb.MsgHeartbeatResp:
		return daproto.MessageType_HEARTBEAT
	default:
		return daproto.MessageType_LOG_ENTRY_COMMITTED
	}
}
