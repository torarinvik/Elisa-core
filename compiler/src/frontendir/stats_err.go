package frontendir

import "errors"

var errBadMagic = errors.New("not a frontend IR bundle (bad magic)")
