package kirocreds

import "os"

func statHelper(p string) (any, error) { return os.Stat(p) }
