package prefixCrypt

import (
	"emperror.dev/errors"
	"io"
)

func NewEncWriter(w io.Writer, encrypt func(src []byte) ([]byte, error)) *EncWriter {
	return &EncWriter{
		w:       w,
		buf:     []byte{},
		encrypt: encrypt,
	}
}

type EncWriter struct {
	w       io.Writer
	buf     []byte
	encrypt func(src []byte) ([]byte, error)
}

func (e EncWriter) Close() error {
	if len(e.buf) > 0 && e.buf != nil {
		enc, err := e.encrypt(e.buf)
		if err != nil {
			return errors.Wrap(err, "cannot encrypt buffer")
		}
		if _, err := e.w.Write(enc); err != nil {
			return errors.WithStack(err)
		}
		e.buf = nil
	}
	return nil
}

func (e EncWriter) Write(p []byte) (n int, err error) {
	rest := 512 - len(e.buf)
	if rest > 0 && e.buf != nil {
		if rest > len(p) {
			rest = len(p)
		}
		e.buf = append(e.buf, p[:rest]...)
		p = p[rest:]
		if len(e.buf) == 512 {
			enc, err := e.encrypt(e.buf)
			if err != nil {
				return 0, errors.Wrap(err, "cannot encrypt buffer")
			}
			n, err = e.w.Write(enc)
			if err != nil {
				return n, errors.WithStack(err)
			}
			e.buf = nil
		}
	}
	x, err := e.w.Write(p)
	if err != nil {
		return n, errors.WithStack(err)
	}
	n += x
	return
}

var _ io.WriteCloser = (*EncWriter)(nil)
