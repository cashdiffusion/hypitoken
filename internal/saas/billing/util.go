package billing

import "os"

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
