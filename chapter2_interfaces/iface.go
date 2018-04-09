package main

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

	// This call just makes sure that the interface is actually used.
	// Without this call, the linker would see that the interface defined above
	// is in fact never used, and thus would optimize it out of the final
	// executable.
	m.Add(10, 32)
}
