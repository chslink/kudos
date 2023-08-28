package server

import (
	"github.com/chslink/kudos/rpcx/protocol"
	"github.com/chslink/kudos/rpcx/share"
)

type MessageEnvelope struct {
	Context *share.Context
	Request *protocol.Message
}

func newMessageEnvelope(ctx *share.Context, req *protocol.Message) *MessageEnvelope {
	return &MessageEnvelope{
		Context: ctx,
		Request: req,
	}
}

type TimeEnvelope struct {
	Session *ServerSession
	Cb      TimeTickCallback
}

func newTimeEnvelope(session *ServerSession, cb TimeTickCallback) *TimeEnvelope {
	return &TimeEnvelope{Session: session, Cb: cb}
}
