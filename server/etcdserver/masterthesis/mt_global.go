package masterthesis

import (
	"go.etcd.io/etcd/server/v3/daproto"
)

const ForceActionType = true
const ForceOnMessageKey = false
const ForcedActionType = daproto.ActionType_RESEND_LAST_MESSAGE_ACTION_TYPE
const ForcedMessageKey = "10000"
const StopForcedMessageKey = "10100"
