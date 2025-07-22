// recovery.go provides panic recovery and logging utilities used across nina projects
package util

import (
	"fmt"
	"log"
	"runtime/debug"
)

// LogRecover recovers from panic and logs the error with stack trace
func LogRecover() {
	if r := recover(); r != nil {
		stack := debug.Stack()
		log.Printf("PANIC recovered: %v\nStack trace:\n%s", r, stack)
	}
}

// LogRecoverTo recovers from panic and logs to the provided logger
func LogRecoverTo(logger *log.Logger) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		if logger != nil {
			logger.Printf("PANIC recovered: %v\nStack trace:\n%s", r, stack)
		} else {
			log.Printf("PANIC recovered: %v\nStack trace:\n%s", r, stack)
		}
	}
}

// RecoverAndLog recovers from panic, logs it, and re-panics
func RecoverAndLog() {
	if r := recover(); r != nil {
		stack := debug.Stack()
		log.Printf("PANIC: %v\nStack trace:\n%s", r, stack)
		panic(r)
	}
}

// RecoverToError recovers from panic and returns it as an error
func RecoverToError(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("panic: %v", r)
	}
}