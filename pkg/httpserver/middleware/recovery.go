package middleware

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	applogger "github.com/labring/sealos-state-metrics/pkg/logger"
	log "github.com/sirupsen/logrus"
)

var (
	dunno     = []byte("???")
	centerDot = []byte("·")
	dot       = []byte(".")
	slash     = []byte("/")
)

// Recovery handles panics similarly to aiproxy's gin recovery middleware, but
// without any notification side effects.
func Recovery(base *log.Entry, serverName string) gin.HandlerFunc {
	if base == nil {
		base = log.WithField("component", "http")
	}

	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				entry := base.WithField("server", serverName)
				if requestEntry, ok := applogger.EntryFromRequest(c.Request); ok {
					entry = requestEntry
				}

				panicErr := recoveredToError(recovered)
				fileLine, stackTrace := stack(3)
				requestDump := dumpRequest(c.Request)
				brokenPipe := isBrokenPipe(recovered)

				c.Error(panicErr).SetType(gin.ErrorTypePrivate)

				switch {
				case brokenPipe:
					entry.WithFields(log.Fields{
						"source":  fileLine,
						"request": requestDump,
					}).Errorf("%v", panicErr)
					c.Abort()
				case gin.IsDebugging():
					entry.WithField("source", fileLine).Errorf(
						"[Recovery] panic recovered:\n%s\n%v\n%s",
						requestDump,
						recovered,
						stackTrace,
					)
					c.AbortWithStatus(http.StatusInternalServerError)
				default:
					entry.WithField("source", fileLine).Errorf(
						"[Recovery] panic recovered:\n%v\n%s",
						recovered,
						stackTrace,
					)
					c.AbortWithStatus(http.StatusInternalServerError)
				}
			}
		}()

		c.Next()
	}
}

func recoveredToError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}

	return fmt.Errorf("%v", recovered)
}

func isBrokenPipe(recovered any) bool {
	ne, ok := recovered.(*net.OpError)
	if !ok {
		return false
	}

	var se *os.SyscallError
	if !errors.As(ne, &se) {
		return false
	}

	seStr := strings.ToLower(se.Error())

	return strings.Contains(seStr, "broken pipe") ||
		strings.Contains(seStr, "connection reset by peer")
}

func dumpRequest(req *http.Request) string {
	httpRequest, _ := httputil.DumpRequest(req, false)

	headers := strings.Split(string(httpRequest), "\r\n")
	for idx, header := range headers {
		current := strings.Split(header, ":")
		if len(current) > 0 && current[0] == "Authorization" {
			headers[idx] = current[0] + ": *"
		}
	}

	return strings.Join(headers, "\r\n")
}

// stack returns a nicely formatted stack frame, skipping skip frames.
func stack(skip int) (fileLine string, stack []byte) {
	buf := new(bytes.Buffer)

	var (
		lines    [][]byte
		lastFile string
	)

	for i := skip; ; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}

		fmt.Fprintf(buf, "%s:%d (0x%x)\n", file, line, pc)

		if fileLine == "" {
			fileLine = fmt.Sprintf("%s:%d", file, line)
		}

		if file != lastFile {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}

			lines = bytes.Split(data, []byte{'\n'})
			lastFile = file
		}

		fmt.Fprintf(buf, "\t%s: %s\n", function(pc), source(lines, line))
	}

	return fileLine, buf.Bytes()
}

func source(lines [][]byte, n int) []byte {
	n--
	if n < 0 || n >= len(lines) {
		return dunno
	}

	return bytes.TrimSpace(lines[n])
}

func function(pc uintptr) []byte {
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return dunno
	}

	name := []byte(fn.Name())
	if lastSlash := bytes.LastIndex(name, slash); lastSlash >= 0 {
		name = name[lastSlash+1:]
	}

	if period := bytes.Index(name, dot); period >= 0 {
		name = name[period+1:]
	}

	name = bytes.ReplaceAll(name, centerDot, dot)

	return name
}
