// Command screengrab takes a picture of a device's screen through its own VNC
// bridge, and writes it as a PNG.
//
// It exists because there are three different answers to "what is on the
// screen" and they can all disagree:
//
//   - what the browser believes it drew, which is what its own screenshot
//     shows, and which is wrong when the window it drew into is not the one
//     on the wall;
//   - what the X server holds, which is wrong when something else owns the
//     display;
//   - what a viewer connecting over VNC sees, which is what a person looking
//     at the screen sees.
//
// This is the third, and it is the one that settles arguments. It speaks
// enough of the remote framebuffer protocol to ask for one uncompressed
// update, which is all a still picture needs.
//
//	go run ./tools/screengrab -url http://device:8080 -password ... -output screen.png
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	address := flag.String("url", "http://127.0.0.1:8080", "the device's web interface")
	password := flag.String("password", "", "the device's administrator password")
	output := flag.String("output", "screen.png", "where to write the picture")
	flag.Parse()

	if err := run(*address, *password, *output); err != nil {
		fmt.Fprintf(os.Stderr, "screengrab: %s\n", err)
		os.Exit(1)
	}
}

func run(address, password, output string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	body, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		return err
	}
	response, err := client.Post(address+"/api/v1/session", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot sign in: %w", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("cannot sign in: %s", response.Status)
	}

	parsed, err := url.Parse(address)
	if err != nil {
		return err
	}
	headers := http.Header{}
	for _, cookie := range jar.Cookies(parsed) {
		headers.Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	headers.Set("Origin", address)

	dialer := websocket.Dialer{HandshakeTimeout: 20 * time.Second, Subprotocols: []string{"binary"}}
	connection, _, err := dialer.Dial("ws"+strings.TrimPrefix(address, "http")+"/api/v1/vnc", headers)
	if err != nil {
		return fmt.Errorf("cannot open the VNC bridge: %w", err)
	}
	defer func() { _ = connection.Close() }()

	stream := &frames{connection: connection}
	picture, err := grab(stream)
	if err != nil {
		return err
	}

	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if err := png.Encode(file, picture); err != nil {
		return err
	}

	bounds := picture.Bounds()
	fmt.Printf("wrote %s (%dx%d)\n", output, bounds.Dx(), bounds.Dy())
	return nil
}

// grab speaks the protocol far enough to receive one full screen.
func grab(stream *frames) (image.Image, error) {
	// The server states its version; answer with the same one.
	version, err := stream.read(12)
	if err != nil {
		return nil, fmt.Errorf("no greeting: %w", err)
	}
	if !bytes.HasPrefix(version, []byte("RFB ")) {
		return nil, fmt.Errorf("that is not the remote framebuffer protocol: %q", version)
	}
	if err := stream.write(version); err != nil {
		return nil, err
	}

	// Security types: a count, then that many bytes. The daemon's server is
	// reached over an authenticated bridge and offers None.
	count, err := stream.read(1)
	if err != nil {
		return nil, err
	}
	if count[0] == 0 {
		return nil, fmt.Errorf("the server refused the connection")
	}
	types, err := stream.read(int(count[0]))
	if err != nil {
		return nil, err
	}
	const securityNone = 1
	chosen := byte(0)
	for _, kind := range types {
		if kind == securityNone {
			chosen = securityNone
		}
	}
	if chosen == 0 {
		return nil, fmt.Errorf("the server wants an authentication this cannot do: %v", types)
	}
	if err := stream.write([]byte{securityNone}); err != nil {
		return nil, err
	}
	result, err := stream.read(4)
	if err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint32(result) != 0 {
		return nil, fmt.Errorf("the server rejected the connection")
	}

	// Ask to share the display, then read what it is.
	if err := stream.write([]byte{1}); err != nil {
		return nil, err
	}
	header, err := stream.read(24)
	if err != nil {
		return nil, err
	}
	width := int(binary.BigEndian.Uint16(header[0:2]))
	height := int(binary.BigEndian.Uint16(header[2:4]))
	// The pixel format is sixteen bytes starting at offset four: bits per
	// pixel, depth, byte order, true colour, then three maximum values of two
	// bytes each, then the three shifts. Getting the offsets wrong produces a
	// picture that is recognisable and the wrong colour, which is a
	// particularly easy mistake to leave in.
	bitsPerPixel := int(header[4])
	bigEndian := header[6] != 0
	redMax := binary.BigEndian.Uint16(header[8:10])
	greenMax := binary.BigEndian.Uint16(header[10:12])
	blueMax := binary.BigEndian.Uint16(header[12:14])
	redShift, greenShift, blueShift := header[14], header[15], header[16]

	nameLength := int(binary.BigEndian.Uint32(header[20:24]))
	if nameLength > 0 {
		if _, err := stream.read(nameLength); err != nil {
			return nil, err
		}
	}
	if bitsPerPixel != 32 {
		return nil, fmt.Errorf("the screen is %d bits a pixel; this only reads 32", bitsPerPixel)
	}
	fmt.Printf("the screen is %dx%d\n", width, height)

	// Raw encoding only: one uncompressed rectangle is all a still picture
	// needs, and it removes every question about the decoder.
	if err := stream.write([]byte{2, 0, 0, 1, 0, 0, 0, 0}); err != nil {
		return nil, err
	}

	// Ask for the whole screen, not an update to it.
	request := make([]byte, 10)
	request[0] = 3
	request[1] = 0
	binary.BigEndian.PutUint16(request[6:8], uint16(width))
	binary.BigEndian.PutUint16(request[8:10], uint16(height))
	if err := stream.write(request); err != nil {
		return nil, err
	}

	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) {
		message, err := stream.read(1)
		if err != nil {
			return nil, err
		}
		if message[0] != 0 {
			// A bell, a clipboard, a colour map: skip whatever it is by
			// reading nothing more and asking again.
			continue
		}
		rest, err := stream.read(3)
		if err != nil {
			return nil, err
		}
		rectangles := int(binary.BigEndian.Uint16(rest[1:3]))

		for index := 0; index < rectangles; index++ {
			descriptor, err := stream.read(12)
			if err != nil {
				return nil, err
			}
			left := int(binary.BigEndian.Uint16(descriptor[0:2]))
			top := int(binary.BigEndian.Uint16(descriptor[2:4]))
			rectangleWidth := int(binary.BigEndian.Uint16(descriptor[4:6]))
			rectangleHeight := int(binary.BigEndian.Uint16(descriptor[6:8]))
			encoding := int32(binary.BigEndian.Uint32(descriptor[8:12]))

			if encoding != 0 {
				return nil, fmt.Errorf("the server used encoding %d; this only reads raw", encoding)
			}

			pixels, err := stream.read(rectangleWidth * rectangleHeight * 4)
			if err != nil {
				return nil, err
			}
			paint(picture, pixels, left, top, rectangleWidth, rectangleHeight,
				bigEndian, redShift, greenShift, blueShift, redMax, greenMax, blueMax)
		}
		return picture, nil
	}
	return nil, fmt.Errorf("the screen never arrived")
}

func paint(picture *image.RGBA, pixels []byte, left, top, width, height int,
	bigEndian bool, redShift, greenShift, blueShift byte, redMax, greenMax, blueMax uint16) {
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			offset := (row*width + column) * 4
			if offset+3 >= len(pixels) {
				return
			}
			var value uint32
			if bigEndian {
				value = binary.BigEndian.Uint32(pixels[offset : offset+4])
			} else {
				value = binary.LittleEndian.Uint32(pixels[offset : offset+4])
			}
			picture.SetRGBA(left+column, top+row, color.RGBA{
				R: component(value, redShift, redMax),
				G: component(value, greenShift, greenMax),
				B: component(value, blueShift, blueMax),
				A: 0xff,
			})
		}
	}
}

// component pulls one colour out of a pixel. The maximum says how many bits
// it has, which is eight on everything this meets but is stated rather than
// assumed.
func component(value uint32, shift byte, maximum uint16) uint8 {
	if maximum == 0 {
		return 0
	}
	raw := (value >> shift) & uint32(maximum)
	return uint8(raw * 255 / uint32(maximum))
}

// frames turns the WebSocket's messages back into a byte stream, since a
// frame is not a meaningful unit of the protocol.
type frames struct {
	connection *websocket.Conn
	pending    []byte
}

func (self *frames) read(count int) ([]byte, error) {
	for len(self.pending) < count {
		_ = self.connection.SetReadDeadline(time.Now().Add(60 * time.Second))
		kind, data, err := self.connection.ReadMessage()
		if err != nil {
			return nil, err
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		self.pending = append(self.pending, data...)
	}
	taken := self.pending[:count]
	self.pending = self.pending[count:]
	return taken, nil
}

func (self *frames) write(data []byte) error {
	_ = self.connection.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return self.connection.WriteMessage(websocket.BinaryMessage, data)
}
