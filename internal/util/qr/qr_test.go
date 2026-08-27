package qr

import "testing"

func TestEncodingProducesASquareWithAQuietBorder(t *testing.T) {
	matrix, err := Encode("http://192.0.2.10:8080/")
	if err != nil {
		t.Fatalf("encode: %s", err)
	}
	if len(matrix) < 21 {
		t.Fatalf("a QR code is at least 21 modules across; this one is %d", len(matrix))
	}
	for row := range matrix {
		if len(matrix[row]) != len(matrix) {
			t.Fatalf("row %d is %d wide but the code is %d tall; it must be square",
				row, len(matrix[row]), len(matrix))
		}
	}

	// The border has to be white all the way round, or scanners struggle.
	last := len(matrix) - 1
	for index := range matrix {
		for _, dark := range []bool{matrix[0][index], matrix[last][index], matrix[index][0], matrix[index][last]} {
			if dark {
				t.Fatalf("the outermost ring has a black module at %d, so the "+
					"quiet border is missing", index)
			}
		}
	}
}

func TestTheThreeFinderPatternsAreThere(t *testing.T) {
	matrix, err := Encode("cue")
	if err != nil {
		t.Fatalf("encode: %s", err)
	}

	// A QR code carries a 7x7 pattern in three of its four corners, which is
	// what a scanner looks for first to find and orient the code. Finding
	// them proves this is a real code and not merely a square of the right
	// size. Where exactly they start depends on the width of the quiet
	// border, so the whole matrix is searched rather than four fixed corners.
	var found [][2]int
	for row := 0; row+7 <= len(matrix); row++ {
		for column := 0; column+7 <= len(matrix); column++ {
			if looksLikeFinder(matrix, row, column) {
				found = append(found, [2]int{row, column})
			}
		}
	}
	if len(found) != 3 {
		t.Fatalf("found %d finder patterns at %v, want exactly 3", len(found), found)
	}

	// And they must be in three different corners, not three in a row.
	middle := len(matrix) / 2
	corners := map[[2]bool]bool{}
	for _, at := range found {
		corners[[2]bool{at[0] > middle, at[1] > middle}] = true
	}
	if len(corners) != 3 {
		t.Errorf("the three finder patterns are not in three different corners: %v", found)
	}
}

// looksLikeFinder reports whether a finder pattern starts exactly here: a
// black 7x7 ring, a white ring inside it, and a black 3x3 core.
func looksLikeFinder(matrix [][]bool, row, column int) bool {
	for rowOffset := 0; rowOffset < 7; rowOffset++ {
		for columnOffset := 0; columnOffset < 7; columnOffset++ {
			edge := rowOffset == 0 || rowOffset == 6 || columnOffset == 0 || columnOffset == 6
			inner := rowOffset >= 2 && rowOffset <= 4 && columnOffset >= 2 && columnOffset <= 4
			if matrix[row+rowOffset][column+columnOffset] != (edge || inner) {
				return false
			}
		}
	}
	return true
}
