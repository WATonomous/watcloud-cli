//go:build !linux

package du

import (
	"context"
	"errors"
	"io"
	"os"
)

func getAttribute(context.Context, *os.File, string) (string, error) {
	return "", errors.New("watcloud du requires Linux")
}

func openStorage(string, attributeReader) (*filesystem, error) {
	return nil, errors.New("watcloud du requires Linux and a CephFS-backed home")
}

func requireTerminal(io.Reader, io.Writer) error {
	return errors.New("watcloud du requires Linux and an interactive terminal")
}

func (*filesystem) load(context.Context, string, *identity) (directory, error) {
	return directory{}, errors.New("watcloud du requires Linux")
}
