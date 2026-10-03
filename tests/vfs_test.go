package tests

import (
	"fmt"
	"reflect"
	"testing"

	"neohome/internal/core"
)

func TestVFSListReturnsSortedDirectChildren(t *testing.T) {
	v := core.NewVFS()
	v.Write("/var/log/z.log", "", 0644, "root", "root")
	v.Write("/var/log/a.log", "", 0644, "root", "root")
	v.Write("/var/log/archive/old.log", "", 0644, "root", "root")

	got := v.List("/var/log")
	want := []string{"/var/log/a.log", "/var/log/archive", "/var/log/z.log"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List(/var/log) = %v, want %v", got, want)
	}

	got = v.List("/")
	want = []string{"/var"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List(/) = %v, want %v", got, want)
	}
}

func BenchmarkVFSList(b *testing.B) {
	v := core.NewVFS()
	for i := 0; i < 4; i++ {
		v.Write(fmt.Sprintf("/bench/target/file-%d", i), "", 0644, "root", "root")
	}
	for i := 0; i < 10_000; i++ {
		v.Write(fmt.Sprintf("/bench/unrelated/file-%d", i), "", 0644, "root", "root")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.List("/bench/target")
	}
}
