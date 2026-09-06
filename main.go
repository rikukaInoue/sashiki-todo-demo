// sashiki のデモ用 TODO アプリ。
// PR ごとに sashiki の DB ブランチ + Lambda (Web Adapter) のプレビュー環境が生える。
// 接続先は環境変数で受け取るだけで、アプリは sashiki の存在を知らない。
package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/go-sql-driver/mysql"
)

//go:embed templates/*.html
var templateFS embed.FS

type Todo struct {
	ID        int64
	Title     string
	Done      bool
	CreatedAt string
}

type PageData struct {
	Todos  []Todo
	Branch string
	DBAddr string
	Error  string
}

type App struct {
	db     *sql.DB
	tmpl   *template.Template
	branch string
	dbAddr string
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func openDB() (*sql.DB, string) {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = env("DB_HOST", "127.0.0.1") + ":" + env("DB_PORT", "3306")
	cfg.User = env("DB_USER", "root") // sashiki のブランチでは dev@pr-<N> 形式
	cfg.Passwd = os.Getenv("DB_PASSWORD")
	cfg.DBName = env("DB_NAME", "todo")
	cfg.ParseTime = false

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(4)
	return db, cfg.Addr
}

func main() {
	db, addr := openDB()
	app := &App{
		db:     db,
		tmpl:   template.Must(template.ParseFS(templateFS, "templates/*.html")),
		branch: env("SASHIKI_BRANCH", "local"),
		dbAddr: addr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.index)
	mux.HandleFunc("POST /todos", app.create)
	mux.HandleFunc("POST /todos/{id}/toggle", app.toggle)
	mux.HandleFunc("POST /todos/{id}/delete", app.delete)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	port := env("PORT", "8080")
	log.Printf("listening on :%s (branch=%s db=%s)", port, app.branch, addr)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func (a *App) render(w http.ResponseWriter, data PageData) {
	data.Branch = a.branch
	data.DBAddr = a.dbAddr
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("render: %v", err)
	}
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(),
		"SELECT id, title, done, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i') FROM todos ORDER BY done, id DESC")
	if err != nil {
		a.render(w, PageData{Error: fmt.Sprintf("DB に接続できません: %v", err)})
		return
	}
	defer rows.Close()

	var todos []Todo
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt); err != nil {
			a.render(w, PageData{Error: err.Error()})
			return
		}
		todos = append(todos, t)
	}
	a.render(w, PageData{Todos: todos})
}

func (a *App) create(w http.ResponseWriter, r *http.Request) {
	title := r.FormValue("title")
	if title != "" {
		if _, err := a.db.ExecContext(r.Context(), "INSERT INTO todos (title) VALUES (?)", title); err != nil {
			a.render(w, PageData{Error: err.Error()})
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) toggle(w http.ResponseWriter, r *http.Request) {
	a.exec(w, r, "UPDATE todos SET done = NOT done WHERE id = ?")
}

func (a *App) delete(w http.ResponseWriter, r *http.Request) {
	a.exec(w, r, "DELETE FROM todos WHERE id = ?")
}

func (a *App) exec(w http.ResponseWriter, r *http.Request, query string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if _, err := a.db.ExecContext(r.Context(), query, id); err != nil {
		a.render(w, PageData{Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
