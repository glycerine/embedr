package embedr

/*
#include "embedr.h"
*/
import "C"

func LastHistoryLine() string {
	return C.GoString(C.last_history())
}
