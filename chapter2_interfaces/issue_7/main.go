package main

import (
	"fmt"

	"github.com/teh-cmc/go-internals/chapter2_interfaces/issue_7/C"
	"github.com/teh-cmc/go-internals/chapter2_interfaces/issue_7/D"
)

func main() {
	fmt.Println(C.Add(10, 32))
	fmt.Println(D.Add(10, 32))
}
