// Package qr turns text into the black and white squares of a QR code.
//
// It exists so that a screen can show something a phone camera can read, which
// is the only input device a person standing in front of a wall display is
// guaranteed to have. The encoding itself is somebody else's work; this is the
// one place that depends on it, so replacing that library later means changing
// this file and nothing else.
package qr

import (
	"errors"

	qrcode "github.com/skip2/go-qrcode"
)

// Encode turns text into a square matrix of modules -- the little squares a QR
// code is made of -- where true is black.
//
// The matrix includes the quiet border of white modules around the code. That
// border is not decoration: a scanner uses it to find where the code begins,
// and a code drawn right up to the edge of its background often will not read
// at all.
func Encode(text string) ([][]bool, error) {
	if text == "" {
		return nil, errors.New("qr: nothing to encode")
	}

	// Medium recovery, which tolerates about 15% of the code being obscured or
	// out of focus. Higher recovery makes the code denser for the same text,
	// and a denser code is harder to read from across a room, which is the
	// distance that matters here.
	code, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return nil, err
	}

	matrix := code.Bitmap()
	if len(matrix) == 0 {
		return nil, errors.New("qr: the encoder returned nothing")
	}
	return matrix, nil
}
