package util

import (
	"fmt"
	"log"
	"strings"

	"github.com/pkg/sftp"
)

func SearchFile(search string, file *sftp.File) (string, error) {
	buf := make([]byte, 1024)
	n, err := file.Read(buf)
	log.Printf("Read %d bytes in search", n)
	if err != nil {
		return "", fmt.Errorf("Error reading file: %s", err)
	}

	str_file := string(buf)
	lines := strings.Split(str_file, "\n")

	var matches []string
	for _, line := range lines {
		if strings.Contains(line, search) {
			matches = append(matches, line)
		}
	}

	log.Printf("%d lines contain match %s\n", len(matches), search)

	return strings.Join(matches, "\n"), nil
}

func Map[T, B any](data []T, mapper func(T) B) []B {
	var updates []B
	for _, d := range data {
		updates = append(updates, mapper(d))
	}

	return updates
}

func Filter[T any](data []T, filterer func(T) bool) []T {
	var filtered []T
	for _, d := range data {
		if filterer(d) {
			filtered = append(filtered, d)
		}
	}

	return filtered
}

func Reduce[T any](data []T, reducer func(a, b T) T) T {
	var accum T
	for _, d := range data {
		accum = reducer(accum, d)
	}

	return accum
}
