package main

//go:noinline
func add(a, b int32) (int32, bool) { return a + b, true }

func main() { add(10, 32) }
