package host

import (
	"testing"
	"time"
)

var Split = split

const (
	BatchOps   = batchOps
	BatchBytes = batchBytes
	MaxOut     = maxOut
	MaxIn      = maxIn
)

// SetHelloWait sets how long a connection may take to prove itself until
// the test ends. Call it before Serve starts: the restore, registered
// first, runs after the cleanup that stops Serve.
func SetHelloWait(t *testing.T, d time.Duration) {
	old := helloWait
	helloWait = d
	t.Cleanup(func() { helloWait = old })
}
