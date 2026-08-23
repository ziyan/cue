package fleet

import (
	"bytes"
	"io"
)

// bytesReader exists so that the import list of fleet.go stays about what the
// package does rather than about how a request body is made.
func bytesReader(content []byte) io.Reader {
	return bytes.NewReader(content)
}
