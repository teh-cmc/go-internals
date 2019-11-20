package main

import "testing"

type Addifier interface{ Add(a, b int32) int32 }

type Adder struct{ id int32 }

type AdderPtr struct{ id int32 }

//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b + adder.id }

//go:noinline
func (adder *AdderPtr) Add(a, b int32) int32 { return a + b + adder.id }

func BenchmarkDirect(b *testing.B) {
	adder := Adder{id: 6754}
	for i := 0; i < b.N; i++ {
		adder.Add(10, 32)
	}
}

func BenchmarkDirectPtr(b *testing.B) {
	adder := &AdderPtr{id: 6754}
	for i := 0; i < b.N; i++ {
		adder.Add(10, 32)
	}
}

func BenchmarkInterface(b *testing.B) {
	var adder Addifier = Adder{id: 6754}
	for i := 0; i < b.N; i++ {
		adder.Add(10, 32)
	}
}

func BenchmarkPtrToInterface(b *testing.B) {
	var adder Addifier = &AdderPtr{id: 43423}
	for i := 0; i < b.N; i++ {
		adder.Add(10, 32)
	}
}

func main() {}
