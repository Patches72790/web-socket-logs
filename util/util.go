package util

import (
	"fmt"
	"log"
	"os"
	"strings"
)

func SearchFile(search string, filename string) (string, error) {
	buf, err := os.ReadFile(filename)
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
