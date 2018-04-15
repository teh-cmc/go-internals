package D

import (
	"github.com/teh-cmc/go-internals/chapter2_interfaces/issue_7/A"
	"github.com/teh-cmc/go-internals/chapter2_interfaces/issue_7/B"
)

func Add(a, b int32) int32 {
	var adder B.Adder = &A.Calc{}
	return adder.Add(a, b)
}
