package tgraw

import "testing"

func BenchmarkPage100(b *testing.B) {
	m := messageFixture()
	b.ReportAllocs()
	for b.Loop() {
		for i := 0; i < 100; i++ {
			if _, err := Marshal(m); err != nil {
				b.Fatal(err)
			}
		}
	}
}
