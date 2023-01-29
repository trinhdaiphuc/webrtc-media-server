package log

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/k0kubun/pp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// const prefix = "src/git.begroup.team/platform-core/"

// ConsoleEncoderName ...
const ConsoleEncoderName = "custom_console"

var (
	ll Logger

	envPatterns []*regexp.Regexp
)

// Logger wraps zap.Logger
type Logger struct {
	*zap.Logger
}

// Short-hand functions for logging.
var (
	Any        = zap.Any
	Bool       = zap.Bool
	Duration   = zap.Duration
	Float64    = zap.Float64
	Int        = zap.Int
	Int64      = zap.Int64
	Skip       = zap.Skip
	String     = zap.String
	Stringer   = zap.Stringer
	Time       = zap.Time
	Uint       = zap.Uint
	Uint32     = zap.Uint32
	Uint64     = zap.Uint64
	Uintptr    = zap.Uintptr
	ByteString = zap.ByteString
)

var logLevelSeverity = map[zapcore.Level]string{
	zapcore.DebugLevel:  "DEBUG",
	zapcore.InfoLevel:   "INFO",
	zapcore.WarnLevel:   "WARNING",
	zapcore.ErrorLevel:  "ERROR",
	zapcore.DPanicLevel: "CRITICAL",
	zapcore.PanicLevel:  "ALERT",
	zapcore.FatalLevel:  "EMERGENCY",
}

// EncodeLevel maps the internal Zap log level to the appropriate Stackdriver
// level.
func EncodeLevel(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(logLevelSeverity[l])
}

// RFC3339NanoTimeEncoder serializes a time.Time to an RFC3339Nano-formatted
// string with nanoseconds precision.
func RFC3339NanoTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format(time.RFC3339Nano))
}

// Error wraps error for zap.Error.
func Error(err error) zapcore.Field {
	if err == nil {
		return Skip()
	}
	return String("error", err.Error())
}

// Interface ...
func Interface(key string, val interface{}) zapcore.Field {
	if val, ok := val.(fmt.Stringer); ok {
		return zap.Stringer(key, val)
	}
	return zap.Reflect(key, val)
}

// Stack ...
func Stack() zapcore.Field {
	return zap.Stack("stack")
}

// Int32 ...
func Int32(key string, val int32) zapcore.Field {
	return zap.Int(key, int(val))
}

// Object ...
var Object = zap.Any

type dd struct {
	v interface{}
}

func (d dd) String() string {
	return pp.Sprint(d.v)
}

// Dump renders object for debugging
func Dump(v interface{}) fmt.Stringer {
	return dd{v}
}

// ShortColorCallerEncoder encodes caller information with sort path filename and enable color.
func ShortColorCallerEncoder(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
	callerStr := caller.TrimmedPath() + ":" + strconv.Itoa(caller.Line)
	enc.AppendString(callerStr)
}

func NewWithName(name string, opts ...zap.Option) Logger {
	return newWithName(name, opts...)
}

func newWithName(name string, opts ...zap.Option) Logger {
	if name == "" {
		_, filename, _, _ := runtime.Caller(1)
		name = filepath.Dir(filename)
	}

	var enabler zap.AtomicLevel
	if e, ok := enablers[name]; ok {
		enabler = e
	} else {
		enabler = zap.NewAtomicLevel()
		enablers[name] = enabler
	}

	setLogLevelFromEnv(name, enabler)

	stacktraceLevel := zap.NewAtomicLevelAt(zapcore.PanicLevel)

	loggerConfig := zap.Config{
		Level:       enabler,
		Development: false,
		Encoding:    "json", //"json",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "timestamp",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "message",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    EncodeLevel,
			EncodeTime:     RFC3339NanoTimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}
	opts = append(opts, zap.AddStacktrace(stacktraceLevel))
	logger, err := loggerConfig.Build(opts...)
	if err != nil {
		panic(err)
	}
	return Logger{logger}
}

// New returns new zap.Logger
func New(opts ...zap.Option) Logger {
	return newWithName("", opts...)
}

var (
	enablers = make(map[string]zap.AtomicLevel)
)

func init() {
	ll = New()

	envLog := os.Getenv("LOG_LEVEL")
	if envLog == "" {
		return
	}

	var lv zapcore.Level
	err := lv.UnmarshalText([]byte(envLog))
	if err != nil {
		panic(err)
	}

	for _, enabler := range enablers {
		enabler.SetLevel(lv)
	}

	var errPattern string
	envPatterns, errPattern = initPatterns(envLog)
	if errPattern != "" {
		ll.Fatal("Unable to parse LOG_LEVEL. Please set it to a proper value.", String("invalid", errPattern))
	}

	ll.Info("Enable debug log", String("LOG_LEVEL", envLog))
}

func initPatterns(envLog string) ([]*regexp.Regexp, string) {
	patterns := strings.Split(envLog, ",")
	result := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		r, err := parsePattern(p)
		if err != nil {
			return nil, p
		}

		result[i] = r
	}
	return result, ""
}

func parsePattern(p string) (*regexp.Regexp, error) {
	p = strings.ReplaceAll(strings.Trim(p, " "), "*", ".*")
	return regexp.Compile(p)
}

func setLogLevelFromEnv(name string, enabler zap.AtomicLevel) {
	for _, r := range envPatterns {
		if r.MatchString(name) {
			enabler.SetLevel(zap.DebugLevel)
		}
	}
}
