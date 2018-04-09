package main

import "testing"

var j uint32
var eface interface{} = uint32(42)

func BenchmarkEfaceToType(b *testing.B) {
	b.Run("switch-small", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			switch v := eface.(type) {
			case int8:
				j = uint32(v)
			case uint32:
				j = uint32(v)
			case int16:
				j = uint32(v)
			default:
				j = v.(uint32)
			}
		}
	})
	b.Run("switch-big", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			switch v := eface.(type) {
			case int8:
				j = uint32(v)
			case int16:
				j = uint32(v)
			case int32:
				j = uint32(v)
			case uint32:
				j = uint32(v)
			case int64:
				j = uint32(v)
			case uint8:
				j = uint32(v)
			case uint16:
				j = uint32(v)
			case uint64:
				j = uint32(v)
			default:
				j = v.(uint32)
			}
		}
	})
}

func main() {}
