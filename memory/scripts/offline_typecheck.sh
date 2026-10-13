#!/usr/bin/env bash
# Offline type-check / build of the WHOLE project in the sandbox (no module proxy).
# Copies the repo to /tmp/_gotest, replaces external deps with tiny stubs, then
# `go vet` + `GOOS=windows go build`. Stubs only need the symbols the code touches;
# if a build error mentions a missing stub symbol, add it to the matching file below.
set -e
SRC=${1:-/home/user/copyhere}
export GOROOT=/home/user/gotool/go PATH=/home/user/gotool/go/bin:$PATH GOPROXY=off GOTOOLCHAIN=local GOSUMDB=off GOFLAGS=-mod=mod
rm -rf /tmp/_gotest && mkdir -p /tmp/_gotest
(cd "$SRC" && tar --exclude=.git --exclude=downloads --exclude=node_modules -cf - .) | (cd /tmp/_gotest && tar -xf -)
cd /tmp/_gotest
rm -f internal/browser/chromedp.go internal/browser/rod.go internal/browser/rod_proxy_auth_test.go
python3 - <<'PY'
import re
p='internal/browser/backend.go'; s=open(p).read()
s=re.sub(r'func New\(name string\) \(Backend, error\) \{.*?\n\}\n', 'func New(name string) (Backend, error) { return nil, nil }\n', s, flags=re.S)
s=re.sub(r'import \((.*?)\)', lambda m: 'import (' + '\n'.join(l for l in m.group(1).split('\n') if 'rod' not in l and 'chromedp' not in l) + ')', s, count=1, flags=re.S)
s=s.replace('\t"fmt"\n','\t_ "fmt"\n')
open(p,'w').write(s)
PY
mkdir -p _stubs/websocket _stubs/utls _stubs/xproxy/proxy _stubs/wails/pkg/runtime _stubs/wails/pkg/options/assetserver _stubs/wails/pkg/options/windows _stubs/badger _stubs/wmi
printf 'module github.com/gorilla/websocket\ngo 1.21\n' > _stubs/websocket/go.mod
cat > _stubs/websocket/ws.go <<'G'
package websocket
import ("context";"net";"net/http";"time")
const TextMessage = 1
type Dialer struct { HandshakeTimeout time.Duration; EnableCompression bool; NetDialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error) }
type Conn struct{}
func (d *Dialer) DialContext(ctx context.Context, u string, h http.Header) (*Conn, *http.Response, error) { return nil, nil, nil }
func (c *Conn) Close() error { return nil }
func (c *Conn) SetReadDeadline(t time.Time) error { return nil }
func (c *Conn) ReadMessage() (int, []byte, error) { return 0, nil, nil }
func (c *Conn) WriteMessage(t int, b []byte) error { return nil }
G
printf 'module github.com/refraction-networking/utls\ngo 1.21\n' > _stubs/utls/go.mod
cat > _stubs/utls/u.go <<'G'
package tls
import ("context";"net")
type Config struct { ServerName string; NextProtos []string }
type ClientHelloID struct{}
var HelloChrome_120 ClientHelloID
type TLSExtension interface{}
type ALPNExtension struct{ AlpnProtocols []string }
type UConn struct { net.Conn; Extensions []TLSExtension }
func UClient(c net.Conn, cfg *Config, id ClientHelloID) *UConn { return &UConn{Conn: c} }
func (u *UConn) BuildHandshakeState() error { return nil }
func (u *UConn) HandshakeContext(ctx context.Context) error { return nil }
G
printf 'module golang.org/x/net\ngo 1.21\n' > _stubs/xproxy/go.mod
cat > _stubs/xproxy/proxy/p.go <<'G'
package proxy
import "net"
type Auth struct{ User, Password string }
type Dialer interface{ Dial(network, addr string) (net.Conn, error) }
func SOCKS5(network, addr string, auth *Auth, forward Dialer) (Dialer, error) { return nil, nil }
G
printf 'module github.com/wailsapp/wails/v2\ngo 1.21\n' > _stubs/wails/go.mod
cat > _stubs/wails/w.go <<'G'
package wails
import "github.com/wailsapp/wails/v2/pkg/options"
func Run(o *options.App) error { return nil }
G
cat > _stubs/wails/pkg/runtime/r.go <<'G'
package runtime
import "context"
type OpenDialogOptions struct { Title, DefaultDirectory string; CanCreateDirectories bool }
func BrowserOpenURL(ctx context.Context, u string) {}
func EventsEmit(ctx context.Context, name string, data ...interface{}) {}
func OpenDirectoryDialog(ctx context.Context, o OpenDialogOptions) (string, error) { return "", nil }
func WindowCenter(ctx context.Context) {}
func WindowSetSize(ctx context.Context, w, h int) {}
G
cat > _stubs/wails/pkg/options/o.go <<'G'
package options
import ("context";"github.com/wailsapp/wails/v2/pkg/options/assetserver";"github.com/wailsapp/wails/v2/pkg/options/windows")
type RGBA struct{ R, G, B, A uint8 }
type App struct {
	Title string; Width, Height, MinWidth, MinHeight, MaxWidth, MaxHeight int
	DisableResize, Fullscreen, Frameless, StartHidden, HideWindowOnClose, AlwaysOnTop bool
	BackgroundColour *RGBA
	AssetServer *assetserver.Options
	OnStartup func(ctx context.Context); OnDomReady func(ctx context.Context); OnShutdown func(ctx context.Context)
	OnBeforeClose func(ctx context.Context) bool
	Bind []interface{}
	Windows *windows.Options
	WindowStartState int
	Logger interface{}; LogLevel int; LogLevelProduction int
}
G
cat > _stubs/wails/pkg/options/assetserver/a.go <<'G'
package assetserver
import ("io/fs";"net/http")
type Options struct { Assets fs.FS; Handler http.Handler; Middleware func(http.Handler) http.Handler }
G
cat > _stubs/wails/pkg/options/windows/w.go <<'G'
package windows
type Theme int
const ( SystemDefault Theme = iota; Dark; Light )
type Options struct { WebviewIsTransparent, WindowIsTranslucent, DisableWindowIcon, DisableFramelessWindowDecorations bool; Theme Theme; WebviewUserDataPath, WebviewBrowserPath string; BackdropType int; ZoomFactor float64; IsZoomControlEnabled bool }
G
printf 'module github.com/dgraph-io/badger/v4\ngo 1.21\n' > _stubs/badger/go.mod
cat > _stubs/badger/b.go <<'G'
package badger
import "errors"
var ErrKeyNotFound = errors.New("nf")
type Logger interface{}
type Options struct { Logger Logger; ValueLogFileSize int64 }
func DefaultOptions(dir string) Options { return Options{} }
func (o Options) WithLogger(l Logger) Options { return o }
type IteratorOptions struct { Prefix []byte; PrefetchValues bool; PrefetchSize int }
var DefaultIteratorOptions = IteratorOptions{}
type DB struct{}
func Open(o Options) (*DB, error) { return &DB{}, nil }
func (d *DB) Close() error { return nil }
func (d *DB) Update(f func(*Txn) error) error { return f(&Txn{}) }
func (d *DB) View(f func(*Txn) error) error { return f(&Txn{}) }
type Txn struct{}
type Item struct{}
type Iterator struct{}
func (t *Txn) Get(k []byte) (*Item, error) { return &Item{}, nil }
func (t *Txn) Set(k, v []byte) error { return nil }
func (t *Txn) Delete(k []byte) error { return nil }
func (t *Txn) NewIterator(o IteratorOptions) *Iterator { return &Iterator{} }
func (i *Item) Value(f func([]byte) error) error { return nil }
func (i *Item) Key() []byte { return nil }
func (i *Item) KeyCopy(b []byte) []byte { return nil }
func (it *Iterator) Close() {}
func (it *Iterator) Rewind() {}
func (it *Iterator) Valid() bool { return false }
func (it *Iterator) Next() {}
func (it *Iterator) Item() *Item { return &Item{} }
func (it *Iterator) Seek(k []byte) {}
G
printf 'module github.com/yusufpapurcu/wmi\ngo 1.21\n' > _stubs/wmi/go.mod
printf 'package wmi\nfunc Query(q string, dst interface{}, args ...interface{}) error { return nil }\n' > _stubs/wmi/w.go
cat >> go.mod <<'G'

replace github.com/gorilla/websocket => ./_stubs/websocket
replace github.com/refraction-networking/utls => ./_stubs/utls
replace golang.org/x/net => ./_stubs/xproxy
replace github.com/wailsapp/wails/v2 => ./_stubs/wails
replace github.com/dgraph-io/badger/v4 => ./_stubs/badger
replace github.com/yusufpapurcu/wmi => ./_stubs/wmi
G
go vet ./internal/bot/... ./internal/model ./internal/api ./internal/proxy ./internal/store ./internal/workerplan ./internal/logwall
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/chitchatui-typecheck.exe . && echo "ROOT BUILD OK (stubbed deps; not a real release binary)"
go test ./internal/bot ./internal/bot/apichat ./internal/workerplan ./internal/logwall
(cd apitest && go test .)
