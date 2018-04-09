package main

import (
	"fmt"
	"unsafe"
)

type Mather interface {
	Add(a, b int32) int32
	Sub(a, b int64) int64
}

type Adder struct{ id int32 }

//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b }

//go:noinline
func (adder Adder) Sub(a, b int64) int64 { return a - b }

func main() {
	m := Mather(Adder{id: 6754})

	iface := (*iface)(unsafe.Pointer(&m))
	fmt.Printf("iface.tab.hash = %#x\n", iface.tab.hash)

}

// simplified definitions of runtime's iface & itab types

type iface struct {
	tab  *itab
	data unsafe.Pointer
}
type itab struct {
	inter uintptr
	_type uintptr
	hash  uint32
	_     [4]byte
	fun   [1]uintptr
}
