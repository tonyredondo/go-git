package pktline

import (
	"errors"
	"fmt"
	"io"

	"github.com/go-git/go-git/v5/utils/trace"
)

var (
	// ErrNegativeCount is returned by Read when the count is negative.
	ErrNegativeCount = errors.New("negative count")

	// ErrShortRead is returned by Read when the count is less than the
	// number of bytes requested.
	ErrShortRead = errors.New("short read")
)

// Reader represents a pktline reader.
type Reader struct {
	r io.Reader

	buf []byte // peeked buffer
}

// NewReader returns a new pktline reader that reads from r and supports
// peeking.
func NewReader(r io.Reader) *Reader {
	rdr := &Reader{
		r: r,
	}
	return rdr
}

// Peek implements ioutil.ReadPeeker.
func (r *Reader) Peek(n int) (b []byte, err error) {
	if n < 0 {
		return nil, ErrNegativeCount
	}

	if n <= len(r.buf) {
		return r.buf[:n], nil
	}

	readLen := n - len(r.buf)
	readBuf := make([]byte, readLen)
	readN, err := r.r.Read(readBuf)
	if err != nil {
		return nil, err
	}

	if readN != readLen {
		err = fmt.Errorf("%w: %d != %d", ErrShortRead, readN, readLen)
	}

	r.buf = append(r.buf, readBuf[:readN]...)
	return r.buf, err
}

// Read implements ioutil.ReadPeeker.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		if n == len(p) {
			return n, nil
		}

		p = p[n:]
	}

	return r.r.Read(p)
}

// PeekPacket returns the next pktline without advancing the reader.
// It returns the pktline length, the pktline payload and an error, if any.
// If the pktline is a flush-pkt, delim-pkt or response-end-pkt, the payload
// will be nil and the length will be the pktline type.
// To get the payload length, subtract the length by the pkt-len size (4).
func (r *Reader) PeekPacket() (l int, p []byte, err error) {
	defer func() {
		if err == nil {
			trace.Packet.Printf("packet: < %04x %s", l, p)
		}
	}()

	npeek := lenSize - len(r.buf)
	if npeek > 0 {
		_, err := r.Peek(npeek)
		if err != nil {
			return Err, nil, err
		}
	}

	length, err := ParseLength(r.buf[:lenSize])
	if err != nil {
		return Err, nil, err
	}

	switch length {
	case Flush, Delim, ResponseEnd:
		return length, nil, nil
	case 4: // empty line
		return length, Empty, nil
	}

	dataLen := length - lenSize
	if len(r.buf) >= lenSize+dataLen {
		return length, r.buf[lenSize : lenSize+dataLen], nil
	}

	_, err = r.Peek(lenSize + dataLen)
	if err != nil {
		return Err, nil, err
	}

	return length, r.buf[lenSize : lenSize+dataLen], nil
}

// ReadPacket reads a pktline from the reader.
// It returns the pktline length, the pktline payload and an error, if any.
// If the pktline is a flush-pkt, delim-pkt or response-end-pkt, the payload
// will be nil and the length will be the pktline type.
// To get the payload length, subtract the length by the pkt-len size (4).
func (r *Reader) ReadPacket() (l int, p []byte, err error) {
	defer func() {
		if err == nil {
			trace.Packet.Printf("packet: < %04x %s", l, p)
		}
	}()

	var pktlen [lenSize]byte
	n, err := io.ReadFull(r, pktlen[:])
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Err, nil, fmt.Errorf("%w: %d", ErrInvalidPktLen, n)
		}

		return Err, nil, err
	}

	if n != lenSize {
		return Err, nil, fmt.Errorf("%w: %d", ErrInvalidPktLen, n)
	}

	length, err := ParseLength(pktlen[:])
	if err != nil {
		return Err, nil, err
	}

	switch length {
	case Flush, Delim, ResponseEnd:
		return length, nil, nil
	case 4: // empty line
		return length, Empty, nil
	}

	dataLen := length - lenSize
	data := make([]byte, 0, dataLen)
	dn, err := io.ReadFull(r, data[:dataLen])
	if err != nil {
		return Err, nil, err
	}

	if dn != dataLen {
		return Err, data, fmt.Errorf("%w: %d", ErrInvalidPktLen, dn)
	}

	// TODO: handle newlines (\n)
	return length, data[:dn], nil
}
