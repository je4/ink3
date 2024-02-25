package prefixCrypt

import (
	"emperror.dev/errors"
	"io"
)

func NewDecryptReader(r io.ReadSeeker, decode func([]byte) ([]byte, error)) (*DecryptReader, error) {
	mr := &DecryptReader{
		rs:     r,
		buffer: make([]byte, 512),
		decode: decode,
	}
	return mr, nil
}

type DecryptReader struct {
	rs     io.ReadSeeker
	buffer []byte
	offset int64
	decode func([]byte) ([]byte, error)
}

func (mr *DecryptReader) Seek(offset int64, whence int) (int64, error) {
	p, err := mr.rs.Seek(offset, whence)
	if err != nil {
		return p, errors.WithStack(err)
	}
	mr.offset = p
	return p, nil
}

func (mr *DecryptReader) Read(p []byte) (n int, err error) {
	rlen := len(p)
	if mr.offset < int64(len(mr.buffer)) {
		if mr.buffer == nil {
			n, err := mr.rs.Read(mr.buffer)
			if err != nil {
				if errors.Is(err, io.EOF) {
					mr.buffer = make([]byte, 0, 512)
				} else {
					return 0, errors.Wrap(err, "failed to read head")
				}
			}
			mr.buffer, err = mr.decode(mr.buffer[:n])
			if err != nil {
				return 0, errors.Wrap(err, "cannot decode buffer")
			}
		}
		n = copy(p, mr.buffer[mr.offset:])
		mr.offset += int64(n)
		mr.offset, err = mr.rs.Seek(mr.offset, io.SeekStart)
		if err != nil {
			return n, errors.WithStack(err)
		}
		if n < rlen {
			nn, err := mr.rs.Read(p[n:])
			if err != nil {
				return n + nn, errors.WithStack(err)
			}
			mr.offset += int64(nn)
			n += nn
		}
		return n, nil
	}
	num, err := mr.rs.Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return num, io.EOF
		}
		return num, errors.WithStack(err)
	}
	mr.offset += int64(num)
	return num, nil
}

var _ io.ReadSeeker = (*DecryptReader)(nil)
