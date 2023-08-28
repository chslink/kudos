package rpc

import (
	"github.com/chslink/kudos/service/codecService"
	"github.com/mitchellh/mapstructure"
)

type Args struct {
	MsgId  int
	MsgReq interface{}
}

func (a *Args) GetObject(t interface{}) error {
	switch a.MsgReq.(type) {
	case []byte:
		_codec := codecService.GetCodecService()
		return _codec.Unmarshal(t, a.MsgReq.([]byte))
	default:
		return mapstructure.Decode(a.MsgReq, t)
	}
}

type Reply struct {
	Code    int
	ErrMsg  string
	MsgResp interface{}
}

// Group message request
type ArgsGroup struct {
	Route   string
	Payload []byte
}

// Group message response
type ReplyGroup struct {
}

// Route msg to the specified node
type CustomerRoute func(session *Session, servicePath, serviceName string) (string, error)
