//go:build linux || darwin
package embedr

/*
#include "embedr.h"

  // requires -lreadline so making it linux only for the moment.
  // update, maybe -ledit on darwin works too, modulo signal troubles.
  char* last_history() {
    if (history_length == 0) {
      return NULL;
    }

    // contrary to the docs, we have to ask for history_length to
    // get the most recent command, not history_length - 1;
    //HIST_ENTRY* h = history_get(history_length-1);
    HIST_ENTRY* h = history_get(history_length);
    if (h == NULL) {
      return NULL;
    }
    return (char*)(h->line);
  }
*/
import "C"

func LastHistoryLine() string {
	return C.GoString(C.last_history())
}
