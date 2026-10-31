// M0 cross-process resource interoperability tool. Keys travel through stdin,
// resource bytes through inherited test-owned files; no production entry uses it.
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jorben/tacet/internal/store"
)

func main() {
	mode := flag.String("mode", "", "encode or verify")
	input := flag.String("input", "", "generated input resource")
	output := flag.String("output", "", "generated output resource")
	flag.Parse()
	if err := run(*mode, *input, *output); err != nil {
		fmt.Fprintln(os.Stderr, "M0_RESOURCE_INTEROP_FAILED")
		os.Exit(1)
	}
}

func run(mode, input, output string) error {
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 66))
	encoded, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if len(encoded) != 65 {
		return fmt.Errorf("key invalid")
	}
	key, err := hex.DecodeString(encoded[:64])
	if err != nil {
		return err
	}
	defer clear(key)
	source, err := os.Open(input)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if mode == "encode" {
		target, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer target.Close()
		writer, err := store.NewResourceWriter(target, key, "m0-cross-process", 16*1024*1024)
		if err != nil {
			return err
		}
		if _, err = io.Copy(writer, source); err != nil {
			return err
		}
		if err = writer.Close(); err != nil {
			return err
		}
		return target.Sync()
	}
	if mode == "verify" {
		resource, err := store.OpenResource(source, info.Size(), key, "m0-cross-process")
		if err != nil {
			return err
		}
		defer resource.Close()
		if err = resource.Verify(); err != nil {
			return err
		}
		if resource.Size() > 16*1024*1024 {
			return fmt.Errorf("limit")
		}
		_, err = io.Copy(os.Stdout, io.NewSectionReader(resource, 0, resource.Size()))
		return err
	}
	return fmt.Errorf("mode invalid")
}
