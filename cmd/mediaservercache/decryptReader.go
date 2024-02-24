package main

import (
	"emperror.dev/errors"
	"io"
)

func NewDecryptReader(r io.ReadSeeker, decode func([]byte) ([]byte, error)) (*DecryptReader, error) {
	mr := &DecryptReader{
		rs:     r,
		buffer: make([]byte, 512),
	}
	return mr, mr.init(decode)
}

type DecryptReader struct {
	rs     io.ReadSeeker
	buffer []byte
	offset int64
}

func (mr *DecryptReader) init(decode func([]byte) ([]byte, error)) error {
	n, err := mr.rs.Read(mr.buffer)
	if err != nil {
		if errors.Is(err, io.EOF) {
			mr.buffer = make([]byte, 512)
			return nil
		}
		return errors.Wrap(err, "failed to read head")
	}
	mr.buffer, err = decode(mr.buffer[:n])
	return errors.WithStack(err)
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
