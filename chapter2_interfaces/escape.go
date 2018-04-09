package main

type Addifier interface{ Add(a, b int32) int32 }

type Adder struct{ id int32 }

//go:noinline
func (adder Adder) Add(a, b int32) int32 { return a + b }

func main() {
	adder := Adder{id: 6754}
	adder.Add(10, 32)
	Addifier(adder).Add(10, 32)
}
