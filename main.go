// sashiki のデモ用 TODO アプリ。
// PR ごとに sashiki の DB ブランチ + プレビュー環境が生える。
//
// 2 つのモードがある:
//   - 単一ブランチ(既定): 環境変数 DB_USER の接続先だけを使う。PR ごとに 1 デプロイ
//   - マルチブランチ(MULTI_BRANCH=true): URL パス /b/<branch>/ からブランチを判定し、
//     DB_USER@<branch> で sashiki プロキシに接続する。アプリは 1 デプロイを共有でき、
//     PR 環境の作成は DB ブランチ(数秒)だけで済む
package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"

	"github.com/go-sql-driver/mysql"
)

//go:embed templates/*.html
var templateFS embed.FS

var branchNameRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

type Todo struct {
	ID        int64
	Title     string
	Done      bool
	CreatedAt string
}

type PageData struct {
	Todos  []Todo
	Base   string // 相対リンクの基準 (<base href>)
	Branch string
	DBAddr string
	Error  string
}

type App struct {
	tmpl   *template.Template
	addr   string // host:port
	user   string // 単一: dev@pr-1 など / マルチ: ベースユーザー(dev)
	pass   string
	dbname string
	multi  bool

	mu  sync.Mutex
	dbs map[string]*sql.DB // branch(単一モードは "") → 接続
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newApp() *App {
	return &App{
		tmpl:   template.Must(template.ParseFS(templateFS, "templates/*.html")),
		addr:   env("DB_HOST", "127.0.0.1") + ":" + env("DB_PORT", "3306"),
		user:   env("DB_USER", "root"),
		pass:   os.Getenv("DB_PASSWORD"),
		dbname: env("DB_NAME", "todo"),
		multi:  env("MULTI_BRANCH", "") == "true",
		dbs:    map[string]*sql.DB{},
	}
}

// dbFor は branch 用の接続プールを返す(マルチブランチでは user@branch で接続)。
func (a *App) dbFor(branch string) (*sql.DB, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if db, ok := a.dbs[branch]; ok {
		return db, nil
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = a.addr
	cfg.User = a.user
	if branch != "" {
		cfg.User = a.user + "@" + branch
	}
	cfg.Passwd = a.pass
	cfg.DBName = a.dbname
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	a.dbs[branch] = db
	return db, nil
}

// reqCtx はリクエストから (branch, base) を解決する。
func (a *App) reqCtx(r *http.Request) (branch, base string, err error) {
	if !a.multi {
		return "", "/", nil
	}
	branch = r.PathValue("branch")
	if !branchNameRe.MatchString(branch) {
		return "", "", fmt.Errorf("invalid branch name")
	}
	return branch, "/b/" + branch + "/", nil
}

func main() {
	app := newApp()
	mux := http.NewServeMux()

	if app.multi {
		mux.HandleFunc("GET /b/{branch}/{$}", app.index)
		mux.HandleFunc("POST /b/{branch}/todos", app.create)
		mux.HandleFunc("POST /b/{branch}/todos/{id}/toggle", app.toggle)
		mux.HandleFunc("POST /b/{branch}/todos/{id}/delete", app.delete)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if r.URL.Path != "/" {
				w.WriteHeader(http.StatusNotFound)
			}
			fmt.Fprintln(w, "sashiki todo demo (multi-branch)")
			fmt.Fprintln(w, "PR のプレビューは /b/pr-<PR番号>/ を開いてください。例: /b/pr-6/")
			fmt.Fprintln(w, "(ブランチ名は英小文字・数字・ハイフンのみ)")
		})
	} else {
		mux.HandleFunc("GET /{$}", app.index)
		mux.HandleFunc("POST /todos", app.create)
		mux.HandleFunc("POST /todos/{id}/toggle", app.toggle)
		mux.HandleFunc("POST /todos/{id}/delete", app.delete)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	port := env("PORT", "8080")
	log.Printf("listening on :%s (multi=%v db=%s user=%s)", port, app.multi, app.addr, app.user)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func (a *App) render(w http.ResponseWriter, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("render: %v", err)
	}
}

// page は branch 表示用の PageData の共通部を作る。
// DB の接続先は内部情報なので、公開ページには SHOW_DB_ADDR=true のとき
// (ローカル開発)しか表示しない。
func (a *App) page(branch, base string) PageData {
	label := branch
	if label == "" {
		label = env("SASHIKI_BRANCH", "local")
	}
	d := PageData{Base: base, Branch: label}
	if env("SHOW_DB_ADDR", "") == "true" {
		d.DBAddr = a.addr
	}
	return d
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	branch, base, err := a.reqCtx(r)
	if err != nil {
		http.Error(w, "ブランチ名が不正です。/b/pr-<PR番号>/ の形式で開いてください(英小文字・数字・ハイフンのみ)", http.StatusNotFound)
		return
	}
	p := a.page(branch, base)
	db, err := a.dbFor(branch)
	if err != nil {
		p.Error = err.Error()
		a.render(w, p)
		return
	}
	rows, err := db.QueryContext(r.Context(),
		"SELECT id, title, done, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i') FROM todos ORDER BY done, id DESC")
	if err != nil {
		p.Error = fmt.Sprintf("DB に接続できません: %v", err)
		a.render(w, p)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt); err != nil {
			p.Error = err.Error()
			a.render(w, p)
			return
		}
		p.Todos = append(p.Todos, t)
	}
	a.render(w, p)
}

func (a *App) create(w http.ResponseWriter, r *http.Request) {
	branch, base, err := a.reqCtx(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if title := r.FormValue("title"); title != "" {
		db, derr := a.dbFor(branch)
		if derr == nil {
			if _, xerr := db.ExecContext(r.Context(), "INSERT INTO todos (title) VALUES (?)", title); xerr != nil {
				p := a.page(branch, base)
				p.Error = xerr.Error()
				a.render(w, p)
				return
			}
		}
	}
	http.Redirect(w, r, base, http.StatusSeeOther)
}

func (a *App) toggle(w http.ResponseWriter, r *http.Request) {
	a.exec(w, r, "UPDATE todos SET done = NOT done WHERE id = ?")
}

func (a *App) delete(w http.ResponseWriter, r *http.Request) {
	a.exec(w, r, "DELETE FROM todos WHERE id = ?")
}

func (a *App) exec(w http.ResponseWriter, r *http.Request, query string) {
	branch, base, err := a.reqCtx(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	db, err := a.dbFor(branch)
	if err == nil {
		if _, xerr := db.ExecContext(r.Context(), query, id); xerr != nil {
			p := a.page(branch, base)
			p.Error = xerr.Error()
			a.render(w, p)
			return
		}
	}
	http.Redirect(w, r, base, http.StatusSeeOther)
}
