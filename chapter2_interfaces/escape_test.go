package main

import "testing"

type Addifier interface{ Add(a, b int32) int32 }

type Adder struct{ id int32 }

//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b }

func BenchmarkDirect(b *testing.B) {
	adder := Adder{id: 6754}
	for i := 0; i < b.N; i++ {
		adder.Add(10, 32)
	}
}

func BenchmarkInterface(b *testing.B) {
	adder := Adder{id: 6754}
	for i := 0; i < b.N; i++ {
		Addifier(adder).Add(10, 32)
	}
}

func main() {}
