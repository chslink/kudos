package connector

import (
	"net"

	"github.com/chslink/kudos/rpc"
)

type Agent interface {
	Write(data *[]byte)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Close()
	UserData() interface{}
	SetUserData(data interface{})
	GetSession() *rpc.Session
	PushMessage(uint32, []byte)
	KickMessage(string)
}

type Connector interface {
	GetSessionMap() *SessionMap
}

type Connection interface {
	OnDisconnect(session *rpc.Session)
}
