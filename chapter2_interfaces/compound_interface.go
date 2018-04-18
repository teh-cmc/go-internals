package main

type Adder interface{ Add(a, b int32) int32 }
type Subber interface{ Sub(a, b int32) int32 }

type Mather interface {
	Adder
	Subber
}

type Calculator struct{ id int32 }

func (c *Calculator) Add(a, b int32) int32 { return a + b }
func (c *Calculator) Sub(a, b int32) int32 { return a - b }

func main() {
	calc := Calculator{id: 6754}
	var m Mather = &calc
	m.Sub(10, 32)
}
