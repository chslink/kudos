package idService

import (
	"sync"

	"github.com/chslink/kudos/config"
)

var node *Node
var once sync.Once

func GenerateID() ID {
	once.Do(func() {
		node, _ = NewNode(config.NodeId)
	})
	return node.Generate()
}
