package logger

import (
	"context"
	stdlog "log"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Options holds logger configuration options
type Options struct {
	Debug  bool
	Level  string
	Format string
}

// Option is a function that configures Options
type Option func(*Options)

type requestLoggerKey struct{}

// WithDebug enables or disables debug mode
func WithDebug(debug bool) Option {
	return func(o *Options) {
		o.Debug = debug
	}
}

// WithLevel sets the log level (debug, info, warn, error)
func WithLevel(level string) Option {
	return func(o *Options) {
		o.Level = level
	}
}

// WithFormat sets the log format (text or json)
func WithFormat(format string) Option {
	return func(o *Options) {
		o.Format = format
	}
}

func resolveOptions(opts ...Option) *Options {
	options := &Options{
		Debug:  false,
		Level:  "info",
		Format: "text",
	}

	// Apply provided options
	for _, opt := range opts {
		opt(options)
	}

	return options
}

func configure(l *log.Logger, options *Options) {
	if l == nil {
		return
	}

	// Set log level based on configuration
	level := strings.ToLower(options.Level)
	switch level {
	case "debug":
		l.SetLevel(log.DebugLevel)
	case "info":
		l.SetLevel(log.InfoLevel)
	case "warn":
		l.SetLevel(log.WarnLevel)
	case "error":
		l.SetLevel(log.ErrorLevel)
	default:
		l.SetLevel(log.InfoLevel)
	}

	// Enable caller reporting in debug mode
	if options.Debug || level == "debug" {
		l.SetReportCaller(true)
	} else {
		l.SetReportCaller(false)
	}

	l.SetOutput(os.Stdout)
	stdlog.SetOutput(l.Writer())

	// Set formatter based on configuration
	if options.Format == "json" {
		l.SetFormatter(&log.JSONFormatter{
			TimestampFormat: time.DateTime,
		})
	} else {
		l.SetFormatter(&log.TextFormatter{
			ForceColors:      true,
			DisableColors:    false,
			ForceQuote:       options.Debug,
			DisableQuote:     !options.Debug,
			DisableSorting:   false,
			FullTimestamp:    true,
			TimestampFormat:  time.DateTime,
			QuoteEmptyFields: true,
		})
	}
}

// New creates a configured logger instance.
func New(opts ...Option) *log.Logger {
	l := log.New()
	configure(l, resolveOptions(opts...))
	return l
}

// InitLog initializes the logger with the given options and keeps the global logger
// aligned for legacy call sites.
func InitLog(l *log.Logger, opts ...Option) *log.Logger {
	options := resolveOptions(opts...)

	if l == nil {
		l = log.New()
	}

	configure(l, options)
	configure(log.StandardLogger(), options)
	stdlog.SetOutput(l.Writer())

	return l
}

// WithComponent returns a logger with component field
func WithComponent(component string) *log.Entry {
	return log.WithField("component", component)
}

// WithFields returns a logger with multiple fields
func WithFields(fields log.Fields) *log.Entry {
	return log.WithFields(fields)
}

// WithEntry stores the request logger in the context.
func WithEntry(ctx context.Context, entry *log.Entry) context.Context {
	if ctx == nil || entry == nil {
		return ctx
	}

	return context.WithValue(ctx, requestLoggerKey{}, entry)
}

// WithRequestEntry returns a cloned request carrying the logger entry in context.
func WithRequestEntry(req *http.Request, entry *log.Entry) *http.Request {
	if req == nil || entry == nil {
		return req
	}

	return req.WithContext(WithEntry(req.Context(), entry))
}

// EntryFromContext returns a request-scoped logger entry if present.
func EntryFromContext(ctx context.Context) (*log.Entry, bool) {
	if ctx == nil {
		return nil, false
	}

	entry, ok := ctx.Value(requestLoggerKey{}).(*log.Entry)

	return entry, ok
}

// EntryFromRequest returns a request-scoped logger entry if present.
func EntryFromRequest(req *http.Request) (*log.Entry, bool) {
	if req == nil {
		return nil, false
	}

	return EntryFromContext(req.Context())
}
