package main

import (
	"fmt"
	"unsafe"
)

//go:linkname zeroVal runtime.zeroVal
var zeroVal uintptr

type eface struct{ _type, data unsafe.Pointer }

func main() {
	x := 42
	var i interface{} = x - x // outsmart the compiler (avoid static inference)

	fmt.Printf("zeroVal = %p\n", &zeroVal)
	fmt.Printf("      i = %p\n", ((*eface)(unsafe.Pointer(&i))).data)
}
