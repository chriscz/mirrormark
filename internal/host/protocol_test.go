package host_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/host"
	"github.com/chriscz/mirrormark/internal/merge"
	"github.com/chriscz/mirrormark/internal/round"
)

func TestMessageRoundTrip(t *testing.T) {
	title := "GitHub"
	in := []host.Msg{
		{V: 1, Type: "hello", DB: "a", Browser: "chrome/141"},
		{V: 1, Type: "tree", Nodes: []round.TreeNode{{ID: "1", Root: "toolbar"}}},
		{V: 1, Type: "apply", Batch: 7, Ops: []merge.Op{{Op: "create", Ref: "n1", Parent: "1", Title: &title}}},
		{V: 1, Type: "applied", Batch: 7, Results: []merge.OpResult{{OK: true, ID: "9"}}},
		{V: 1, Type: "status", Problems: []host.Problem{{Code: "union-skipped", Message: "m"}}},
	}
	for _, want := range in {
		b, err := host.Encode(want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := host.Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

// Both browsers refuse a message to the extension over 1 MB.
func TestEncodeRefusesOversize(t *testing.T) {
	m := host.Msg{V: 1, Type: "error", Message: strings.Repeat("x", host.MaxOut)}
	if b, err := host.Encode(m); err == nil {
		t.Fatalf("encoded a message of %d bytes", len(b))
	}
	// Just under the limit goes.
	b, _ := json.Marshal(host.Msg{V: 1, Type: "error"})
	m.Message = strings.Repeat("x", host.MaxOut-len(b)-len(`,"message":""`))
	if _, err := host.Encode(m); err != nil {
		t.Fatalf("refused a message of exactly 1 MB: %v", err)
	}
}

func TestDecodeRefusesBadJSON(t *testing.T) {
	if _, err := host.Decode([]byte("{x}")); err == nil || !strings.Contains(err.Error(), "bad message") {
		t.Fatalf("err %v", err)
	}
}

func TestSplitHonoursOpCount(t *testing.T) {
	ops := make([]merge.Op, 2*host.BatchOps+1)
	for i := range ops {
		ops[i] = merge.Op{Op: "remove", ID: "1"}
	}
	batches, err := host.Split(ops)
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, b := range batches {
		sizes = append(sizes, len(b))
	}
	if !reflect.DeepEqual(sizes, []int{host.BatchOps, host.BatchOps, 1}) {
		t.Fatalf("batch sizes %v", sizes)
	}
}

func TestSplitHonoursByteBudget(t *testing.T) {
	big := strings.Repeat("x", 100<<10)
	var ops []merge.Op
	for i := range 25 {
		title := big + string(rune('a'+i))
		ops = append(ops, merge.Op{Op: "create", Ref: "n", Parent: "1", Title: &title})
	}
	batches, err := host.Split(ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) < 3 {
		t.Fatalf("%d batches for 2.5 MB of ops", len(batches))
	}
	var all []merge.Op
	for _, b := range batches {
		enc, _ := json.Marshal(b)
		if len(enc) > host.BatchBytes {
			t.Fatalf("a batch of %d bytes is over the %d budget", len(enc), host.BatchBytes)
		}
		// Room for the rest of the message: it must fit Chrome's limit.
		if _, err := host.Encode(host.Msg{V: 1, Type: "apply", Batch: 1 << 30, Ops: b}); err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	if !reflect.DeepEqual(all, ops) {
		t.Fatal("the batches do not hold the ops in order")
	}
}

func TestSplitRefusesAnOpTooLargeToSend(t *testing.T) {
	title := strings.Repeat("x", host.BatchBytes)
	if _, err := host.Split([]merge.Op{{Op: "create", Title: &title}}); err == nil {
		t.Fatal("split an op larger than a message")
	}
}
