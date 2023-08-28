package json

import (
	"encoding/json"
)

type JsonCodec struct {
}

func NewCodec() *JsonCodec {
	p := new(JsonCodec)
	return p
}

// goroutine safe
func (p *JsonCodec) Unmarshal(obj interface{}, data []byte) error {
	err := json.Unmarshal(data, obj)
	if err != nil {
		return err
	}

	return nil
}

// goroutine safe
func (p *JsonCodec) Marshal(msg interface{}) ([]byte, error) {
	data, err := json.Marshal(msg)
	return data, err
}
