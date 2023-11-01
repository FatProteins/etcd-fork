package masterthesis

import (
	"go.etcd.io/etcd/server/v3/daproto"
)

const ForceActionType = true
const ForceOnMessageKey = false
const ForcedActionType = daproto.ActionType_RESEND_LAST_MESSAGE_ACTION_TYPE
const ForcedMessageKey = "crash"
const StopForcedMessageKey = "10100"

var NodesToCrash = map[uint64]string{
	0xab662f865c0696a1: "crash",
	0xdfdc938d196b5c46: "crash2",
}
