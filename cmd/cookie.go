package cmd

import (
	"encoding/binary"
	"fmt"
	"os"
)

// readCookie pulls the first MIT-MAGIC-COOKIE-1 out of an X authority file, so
// that the command line tools can connect to a display the daemon is driving.
// The format is a sequence of records, each a family followed by four
// length-prefixed strings; see internal/util/xauth, which writes it.
func readCookie(filename string) ([]byte, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("cannot read the X authority file %s: %w", filename, err)
	}

	offset := 0
	for offset+2 <= len(content) {
		offset += 2 // the family
		var fields [4][]byte
		valid := true
		for index := range fields {
			if offset+2 > len(content) {
				valid = false
				break
			}
			length := int(binary.BigEndian.Uint16(content[offset:]))
			offset += 2
			if offset+length > len(content) {
				valid = false
				break
			}
			fields[index] = content[offset : offset+length]
			offset += length
		}
		if !valid {
			break
		}
		if string(fields[2]) == "MIT-MAGIC-COOKIE-1" {
			return fields[3], nil
		}
	}
	return nil, fmt.Errorf("%s has no MIT-MAGIC-COOKIE-1 entry in it", filename)
}
