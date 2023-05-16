package main

//
// pr
//
// Инструмент для переключения между сессиями tmux, привязанными к директориям-"проектам".
//
// Использование:
//
// * pr (без параметров)
//
//   печатает список открытых сессий tmux
//
//   флаг -a добавляет к списку неактивные сессии, которые были открыты ранее.
//
// * pr <каталог или имя сессии>
//
//   переключается на сессию, предварительно её создавая, если её нет.
//
//   В качестве аргумента можно указывать:
//   - абсолютный путь к существующуему каталогу проекта
//   - абсолютный путь к каталогу внутри /tmp, не обязательно существующему (например /tmp/1)
//   - имя подкаталога внутри домашней директории пользователя
//   - префикс имени подкаталога внутри домашней директории пользователя
//   - точку (текущий каталог)
//   - имя сессии tmux или префикс имени
//   - имя из сессии, сохранённой в конфиге ~/.config/pr.yaml
//   - дефис (pr -) переключает на предыдущую сессию
//
// * pr -T
//
//   создаёт временный проект-директорию /tmp/tN (где N это порядковый номер).
//
// * pr -edit
//
//   открывает редактор с конфигом pr (историю открывавшихся сессий)
//
// * pr -todo
//
//   открывает редактор файла .todo в корне текущего проекта.
//
//   посмотреть содержимое всех .todo можно, выполнив pr -w
//
//
// Добавить переключалку в tmux: допишите в ~/.tmux.conf строку:
//
//   bind P display-popup -E -E "pr --interactive"

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/rodaine/table"
)

const (
	VERSION = "v0.1"
)

var (
	fAllowCreateDir  = flag.Bool("c", false, "create project dir if not exists")
	fTempProject     = flag.Bool("T", false, "create temporary project /tmp/tN")
	fWide            = flag.Bool("w", false, "wide output: print all columns")
	fEditConfig      = flag.Bool("edit", false, "open pr config in text editor")
	fShowAllSessions = flag.Bool("a", false, "show all sessions (including saved and inactive)")
	fInteractive     = flag.Bool("interactive", false, "interactive mode for using with tmux: show all sessions then allow user to choose one of them or exit")
	fTodo            = new(bool)
	fVersion         = flag.Bool("version", false, "show pr version")
)

func init() {
	flag.BoolVar(fTodo, "todo", false, "edit TODO file for current project")
	flag.BoolVar(fTodo, "t", false, "edit TODO file for current project")
}

var (
	Home       string
	ConfigPath string
	Config     FavouritesConfig
)

func init() {
	u, _ := user.Current()
	Home = u.HomeDir
	ConfigPath = filepath.Join(Home, ".config", "pr.json")
	Config.Load()
}

func dieIfError(err error) {
	if err != nil {
		log.Panicf("Got error: %s", err)
	}
}

// FavouriteSession это сессия, запомненная в истории / конфиге
type FavouriteSession struct {
	Name    string            `json:"name"`
	Path    string            `json:"path"`
	Cmd     string            `json:"cmd"` // команда, выполняющаяся при старте сессии
	Aliases []string          `json:"aliases"`
	Env     map[string]string `json:"env"` // переменные окружения, с которыми стартует сессия
}

// TmuxSession это сессия в живом tmux
type TmuxSession struct {
	Name         string
	Attached     bool
	LastActivity time.Time
	WindowsCount int
	Path         string
}

func (ts *TmuxSession) String() string {
	return fmt.Sprintf("%s: %s, %d windows %s%s", ts.Name, ts.Path, ts.WindowsCount, ts.FmtLastActivity(), ts.FmtAttached())
}

func (ts *TmuxSession) FmtLastActivity() string {
	t := ""
	if !ts.LastActivity.IsZero() {
		dt := time.Since(ts.LastActivity)
		if dt < 24*time.Hour {
			t = ts.LastActivity.Format("15:04:05")
		} else {
			t = ts.LastActivity.Format("Jan 02")
		}
	}
	return t
}

func (ts *TmuxSession) FmtAttached() string {
	if ts.Attached {
		return "*"
	}
	return ""
}

type FavouritesConfig struct {
	Sessions []FavouriteSession `json:"sessions"`
	changed  bool
}

func (fc *FavouritesConfig) Load() {
	bs, err := os.ReadFile(ConfigPath)
	if err != nil {
		return
	}
	err = json.Unmarshal(bs, fc)
	dieIfError(err)
	fc.changed = false
}

func (fc *FavouritesConfig) Save() {
	if !fc.changed {
		return
	}
	bs, err := json.MarshalIndent(fc, "", "    ")
	dieIfError(err)
	err = os.WriteFile(ConfigPath, bs, 0640)
	dieIfError(err)
}

// Touch добавляет сессию в историю сессий (или переставляет её на первую позицию, если сессия уже была там)
func (fc *FavouritesConfig) Touch(name string, path string) {
	if strings.HasPrefix(path, "/tmp/") {
		// не будем сохранять временные сессии в конфиге
		return
	}
	fs := FavouriteSession{
		Name:    name,
		Path:    path,
		Aliases: []string{},
		Env:     make(map[string]string),
	}
	found_i := -1
	for i, fs1 := range fc.Sessions {
		if fs1.Name == name {
			found_i = i
			break
		}
	}

	if found_i == 0 {
		// nothing to change
		return
	} else if found_i > 0 {
		fs = fc.Sessions[found_i]
	}
	// переставляем сессию на позицию 0
	newOrder := make([]FavouriteSession, 0, len(fc.Sessions)+1)
	newOrder = append(newOrder, fs)
	if found_i >= 0 {
		newOrder = append(newOrder, fc.Sessions[0:found_i]...)
		newOrder = append(newOrder, fc.Sessions[found_i+1:len(fc.Sessions)]...)
	} else {
		newOrder = append(newOrder, fc.Sessions...)
	}
	fc.Sessions = newOrder
	fc.changed = true
}

// TmuxSession возвращает полузаполненный объект TmuxSession
func (f *FavouriteSession) TmuxSession() TmuxSession {
	return TmuxSession{
		Name: f.Name,
		Path: f.Path,
	}
}

// listSessions возвращает список имеющихся сессий tmux
func listSessions() []TmuxSession {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#S\t#{session_path}\t#{session_attached}\t#{session_windows}\t#{session_activity}").CombinedOutput()
	if err != nil {
		log.Printf("tmux list-sessions: %s: %s", err, out)
		return []TmuxSession{}
	}

	sessions := []TmuxSession{}

	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) == 5 {
			s := TmuxSession{
				Name:     parts[0],
				Path:     parts[1],
				Attached: parts[2] != "0",
			}

			n, err := strconv.Atoi(parts[3])
			if err == nil {
				s.WindowsCount = n
			}
			ts, err := strconv.ParseInt(parts[4], 10, 64)
			if err == nil {
				s.LastActivity = time.Unix(int64(ts), 0)
			}

			sessions = append(sessions, s)
		}
	}

	return sessions
}

// createSession создаёт сессию с указанным именем и рабочим каталогом и переключается на неё
func createSession(name string, path string, startCmd string, env map[string]string) {
	args := []string{"new", "-c", path, "-s", name, "-d"}
	for k, v := range env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	if startCmd != "" {
		// это последний аргумент при вызове
		args = append(args, startCmd)
	}
	_, err := exec.Command("tmux", args...).Output()
	dieIfError(err)
}

// switchToSession переключается на сессию с указанным именем
func switchToSession(name string) {
	if os.Getenv("TMUX") != "" {
		out, err := exec.Command("tmux", "switch-client", "-t", name).CombinedOutput()
		if err != nil {
			log.Printf("failed: %s", string(out))
			dieIfError(err)
		}
	} else {
		tmuxPath, err := exec.LookPath("tmux")
		dieIfError(err)
		env := os.Environ()
		err = syscall.Exec(tmuxPath, []string{"tmux", "attach", "-t", name}, env)
		dieIfError(err)
	}
}

// getSessionPath возвращает каталог, с которым была запущена текущая сессия
func getSessionPath() string {
	// tmux display-message -p '#{session_path}'
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_path}").Output()
	dieIfError(err)
	p := string(out)
	return strings.TrimSpace(p)
}

// getTodoFilename возвращает путь к файлу TODO в указанном проекте
func getTodoFilename(dir string) string {
	return filepath.Join(dir, ".todo")
}

// getTodoContents возвращает содержимое TODO в указанном проекте
func getTodoContents(dir string) string {
	fname := getTodoFilename(dir)
	if isFile(fname) {
		bs, err := os.ReadFile(fname)
		dieIfError(err)
		return string(bs)
	}
	return ""
}

// isDir возвращает true, если path это существующий каталог
func isDir(path string) bool {
	if s, err := os.Stat(path); err == nil {
		return s.IsDir()
	}
	return false
}

// isFile возвращает true, если path это существующий файл
func isFile(path string) bool {
	if s, err := os.Stat(path); err == nil {
		return s.Mode().IsRegular()
	}
	return false
}

// readLine читает одну строку из stdin
func readLine() string {
	s := bufio.NewScanner(os.Stdin)
	if ok := s.Scan(); !ok {
		log.Fatal(s.Err())
	}
	return s.Text()
}

// countRepeatedChars возвращает длину строки, если строка состоит только из
// символов char; иначе возвращает 9
func countRepeatedChars(s string, char rune) int {
	for _, ch := range s {
		if ch != char {
			return 0
		}
	}
	return len(s)
}

// createTemporaryProject создаёт временную папку в tmp и возвращает её путь
func createTemporaryProject() string {
	maxNumber := 1024
	for i := 0; i < maxNumber; i++ {
		path := fmt.Sprintf("/tmp/t%d", i)
		err := os.Mkdir(path, 0750)
		if err != nil && os.IsExist(err) {
			continue
		}
		if err != nil {
			log.Fatalf("cannot create temporary directory: %s", err)
		}
		return path
	}
	log.Fatalf("reached max number of temporary projects (%d). Please clean your /tmp/t* folders.", maxNumber)
	return ""
}

var SUFFIXES = []string{"", "1", "2", "3", "4", "5", "6", "7", "8", "9"}

// ChangeSession переключается на сессию sessionId, создавая её, если её ещё нет
func ChangeSession(sessions []TmuxSession, sessionId string, allowCreateDir bool) {
	sessionsByName := make(map[string]TmuxSession)
	for _, s := range sessions {
		sessionsByName[s.Name] = s
	}

	sessionDirPath := ""
	sessionName := ""
	sessionStartCmd := ""
	var sessionEnv map[string]string = nil

	if sessionId == "." {
		x, err := os.Getwd()
		dieIfError(err)
		sessionId = x
	}
	if strings.HasPrefix(sessionId, "/") {
		if !isDir(sessionId) {
			if isDir(filepath.Dir(sessionId)) {
				if allowCreateDir || strings.HasPrefix(sessionId, "/tmp/") {
					err := os.Mkdir(sessionId, os.ModePerm)
					dieIfError(err)
					sessionDirPath = sessionId
				} else {
					log.Fatalf("cannot switch to %s (directory does not exist): use -c flag to create a new directory", sessionId)
				}
			} else {
				log.Fatalf("cannot switch to %s: looks like a dir but does not exist and cannot be created", sessionId)
			}
		} else {
			sessionDirPath = sessionId
		}
		sessionName = filepath.Base(sessionDirPath)
	} else if n := countRepeatedChars(sessionId, '-'); n > 0 {
		// переключаемся на предпоследнюю, или пред-предпоследнюю, или пред-пред<...> сессию
		if len(sessions) < 2 {
			log.Fatalf("cannot switch to a previous session (too few sessions)")
		}
		if n >= len(sessions) {
			n = len(sessions) - 1
		}
		sortedSessions := make([]TmuxSession, len(sessions))
		copy(sortedSessions, sessions)
		sort.Slice(sortedSessions, func(i, j int) bool {
			return sortedSessions[i].LastActivity.After(sortedSessions[j].LastActivity)
		})
		s := sortedSessions[n]
		sessionName = s.Name
		sessionDirPath = s.Path
	} else {
		// ищем по точному совпадению
		if s, ok := sessionsByName[sessionId]; ok {
			sessionName = s.Name
			sessionDirPath = s.Path
		}
		if sessionName == "" {
			// попробуем найти по префиксу
			for _, s := range sessions {
				if strings.HasPrefix(s.Name, sessionId) {
					sessionName = s.Name
					sessionDirPath = s.Path
				}
			}
		}
		if sessionName == "" {
			// заглянем в конфиг и найдём каталог из "избранного"
			// по полному совпадению имени или алиаса
			for _, fs := range Config.Sessions {
				if fs.Name == sessionId {
					sessionName = fs.Name
					sessionDirPath = fs.Path
					sessionStartCmd = fs.Cmd
					sessionEnv = fs.Env
					break
				}
				for _, a := range fs.Aliases {
					if a == sessionId {
						sessionName = fs.Name
						sessionDirPath = fs.Path
						sessionStartCmd = fs.Cmd
						sessionEnv = fs.Env
						break
					}
				}
				if sessionName != "" {
					break
				}
			}
		}
		if sessionName == "" {
			// заглянем в конфиг и найдём каталог из "избранного"
			// теперь уже по префиксу
			for _, fs := range Config.Sessions {
				if strings.HasPrefix(fs.Name, sessionId) {
					sessionName = fs.Name
					sessionDirPath = fs.Path
					sessionStartCmd = fs.Cmd
					sessionEnv = fs.Env
					break
				}
			}
			// алиасы сравнивать по префиксу не будем. Алиасы предполагаются
			// достаточно короткими, чтобы их можно было вводить целиком
		}
	}
	if sessionName == "" {
		// попробуем найти каталог в домашней директории, по точному совпадению
		p := filepath.Join(Home, sessionId)
		if isDir(p) {
			sessionDirPath = p
			sessionName = filepath.Base(sessionDirPath)
		}
	}
	if sessionName == "" {
		// попробуем найти каталог в домашней директории, по префиксу
		entries, err := os.ReadDir(Home)
		dieIfError(err)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), sessionId) {
				p := filepath.Join(Home, e.Name())
				if isDir(p) {
					sessionDirPath = p
					sessionName = filepath.Base(sessionDirPath)
					break
				}
			}
		}
	}
	if sessionName == "" {
		log.Fatalf("directory ~/%s* does not exist", sessionId)
	}

	// подберём имя сессии с суффиксом во избежание коллизий
	for i := 0; i < len(SUFFIXES); i++ {
		_name := sessionName + SUFFIXES[i]
		s, ok := sessionsByName[_name]
		if ok && s.Path == sessionDirPath {
			Config.Touch(s.Name, s.Path)
			switchToSession(s.Name)
			return
		}
		if !ok {
			Config.Touch(sessionName, sessionDirPath)
			createSession(sessionName, sessionDirPath, sessionStartCmd, sessionEnv)
			switchToSession(sessionName)
			return
		}
	}
	log.Fatalf("cannot create session %s because names %s, %s..%s are occupied",
		sessionName,
		sessionName,
		sessionName+SUFFIXES[1],
		sessionName+SUFFIXES[len(SUFFIXES)-1],
	)
}

// openTodoEditor открывает текстовый редактор для TODO-файла
func openTodoEditor() {
	dir := getSessionPath()
	fname := getTodoFilename(dir)
	openFileInEditor(fname)
}

// openFileInEditor открывает текстовый редактор с указанным файлом
func openFileInEditor(filename string) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	editorPath, err := exec.LookPath(editor)
	if err != nil {
		log.Fatalf("cannot locate editor: %s", err)
	}

	env := os.Environ()
	err = syscall.Exec(editorPath, []string{editor, filename}, env)
	dieIfError(err)
}

// printSessions выводит список сессий на экран
func printSessions(sessions []TmuxSession, allColumns bool) {
	cols := []interface{}{"name", "path", "windows", "activity", "attchd"}
	if allColumns {
		cols = append(cols, "todo")
	}

	allSessions := make([]TmuxSession, 0, len(sessions)+len(Config.Sessions))
	allSessions = append(allSessions, sessions...)
	sessionNames := make(map[string]bool)
	for _, s := range allSessions {
		sessionNames[s.Name] = true
	}

	if *fShowAllSessions {
		for _, fs := range Config.Sessions {
			if !sessionNames[fs.Name] {
				allSessions = append(allSessions, fs.TmuxSession())
			}
		}
	}

	tbl := table.New(cols...)
	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()
	tbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for _, s := range allSessions {
		row := []interface{}{s.Name, s.Path, s.WindowsCount, s.FmtLastActivity(), s.FmtAttached()}
		if allColumns {
			todo := getTodoContents(s.Path)
			row = append(row, todo)
		}
		tbl.AddRow(row...)
	}
	tbl.Print()
}

func main() {
	flag.Parse()

	if *fVersion {
		fmt.Printf("%s\n", VERSION)
		return
	}

	if *fTodo {
		openTodoEditor()
		return
	}

	if *fEditConfig {
		openFileInEditor(ConfigPath)
		return
	}

	ss := listSessions()

	sessionId := ""

	if *fTempProject {
		sessionId = createTemporaryProject()
	}

	args := flag.Args()
	if len(args) > 0 {
		sessionId = args[0]
	}

	if *fInteractive {
		printSessions(ss, *fWide)
		fmt.Printf("input project name to switch to: ")
		line := readLine()
		if line == "" {
			return
		}
		sessionId = line
	}

	if sessionId != "" {
		ChangeSession(ss, sessionId, *fAllowCreateDir)
		Config.Save()
		return
	}
	printSessions(ss, *fWide)
}
