package tackle

import "log"

type Logger interface {
	Infof(string, ...interface{})
	Errorf(string, ...interface{})
}

type defaultLogger struct{}

func (l *defaultLogger) Infof(format string, a ...interface{}) {
	log.Printf("TACKLE: "+format, a...)
}

func (l *defaultLogger) Errorf(format string, a ...interface{}) {
	log.Printf("(error) TACKLE: "+format, a...)
}
