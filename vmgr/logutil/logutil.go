package logutil

import "github.com/sirupsen/logrus"

type PrefixFormatter struct {
	logrus.Formatter
	prefix []byte
}

func (f *PrefixFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	orig, err := f.Formatter.Format(entry)
	if err != nil {
		return nil, err
	}

	return append(f.prefix, orig...), nil
}

func NewPrefixFormatter(formatter logrus.Formatter, prefix string) *PrefixFormatter {
	return &PrefixFormatter{
		Formatter: formatter,
		prefix:    []byte(prefix),
	}
}
