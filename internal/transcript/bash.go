package transcript

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// maxBashReadBytes is the largest file whose lines a Bash read counts.
const maxBashReadBytes = 2 << 20

// bashRead is a file that a Bash command reads with cat, head, tail, sed -n,
// or a grep of named files. Lines are 1-based; a range end of 0 means the
// last line of the file.
type bashRead struct {
	path string
	// arg is the path as the command wrote it, which a search prints before
	// each match when it searches more than one file.
	arg    string
	ranges [][2]int
	// last is the number of lines at the end of the file that tail prints.
	last int
	// search is true for grep and rg, whose lines come from their output.
	search bool
	single bool
}

// nonLiteral stands for a word with an expansion, a glob, or a substitution,
// which names no file that the parser can know.
const nonLiteral = "\x00"

// bashReads returns the files under no particular root that command reads,
// resolving relative paths against cwd. It recognizes only commands that
// name regular files that exist; everything else reads nothing.
func bashReads(command string, cwd string) []bashRead {
	var reads []bashRead
	for _, words := range shellCommands(command) {
		for len(words) > 0 && isAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		name := filepath.Base(words[0])
		args := words[1:]
		if name == "cd" {
			cwd = changeDir(cwd, args)
			continue
		}
		files := func() []bashRead {
			var found []bashRead
			for _, arg := range args {
				if strings.HasPrefix(arg, "-") {
					continue
				}
				if path := resolveFile(cwd, arg); path != "" {
					found = append(found, bashRead{path: path, arg: arg})
				}
			}
			return found
		}
		switch name {
		case "cat", "nl", "less", "more", "bat", "batcat":
			for _, read := range files() {
				read.ranges = [][2]int{{1, 0}}
				reads = append(reads, read)
			}
		case "head":
			count := lineOption(args, 10)
			for _, read := range files() {
				read.ranges = [][2]int{{1, count}}
				if count <= 0 {
					read.ranges = [][2]int{{1, 0}}
				}
				reads = append(reads, read)
			}
		case "tail":
			count, from := tailOption(args)
			for _, read := range files() {
				if from > 0 {
					read.ranges = [][2]int{{from, 0}}
				} else {
					read.last = count
				}
				reads = append(reads, read)
			}
		case "sed":
			ranges, ok := sedPrintRanges(args)
			if !ok {
				continue
			}
			for _, read := range files() {
				read.ranges = ranges
				reads = append(reads, read)
			}
		case "rg", "grep", "egrep", "fgrep":
			found := files()
			for _, read := range found {
				read.search = true
				read.single = len(found) == 1
				reads = append(reads, read)
			}
		}
	}
	return reads
}

// lines returns the lines that the read covers, from the file on disk or,
// for a search, from the command's output.
func (r bashRead) lines(stdout string) []int {
	if r.search {
		return searchLines(stdout, r.arg, r.single)
	}
	info, err := os.Stat(r.path)
	if err != nil || info.Size() > maxBashReadBytes {
		return nil
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil
	}
	count := lineCount(string(data))
	ranges := r.ranges
	if r.last > 0 {
		ranges = [][2]int{{max(1, count-r.last+1), 0}}
	}
	seen := make(map[int]bool)
	var lines []int
	for _, span := range ranges {
		end := span[1]
		if end == 0 || end > count {
			end = count
		}
		for line := span[0]; line <= end; line++ {
			if !seen[line] {
				seen[line] = true
				lines = append(lines, line)
			}
		}
	}
	return lines
}

var matchLine = regexp.MustCompile(`^(\d+):`)

// searchLines returns the line numbers of the matches that grep -n or rg -n
// printed for the file that the command named as arg. A search of one file
// prints "N:text", of several files "arg:N:text". Context lines use "-" and
// do not count.
func searchLines(stdout string, arg string, single bool) []int {
	var lines []int
	for _, line := range strings.Split(stdout, "\n") {
		rest, prefixed := strings.CutPrefix(line, arg+":")
		if !prefixed && !single {
			continue
		}
		if !prefixed {
			rest = line
		}
		if match := matchLine.FindStringSubmatch(rest); match != nil {
			if number, err := strconv.Atoi(match[1]); err == nil {
				lines = append(lines, number)
			}
		}
	}
	return lines
}

func isAssignment(word string) bool {
	name, _, found := strings.Cut(word, "=")
	return found && name != "" && !strings.ContainsAny(name, "/-.") && word != nonLiteral
}

// changeDir returns the directory that cd args leaves the shell in, or "" when
// it cannot tell.
func changeDir(cwd string, args []string) string {
	if len(args) == 0 {
		home, _ := os.UserHomeDir()
		return home
	}
	if args[0] == nonLiteral || args[0] == "-" {
		return ""
	}
	target := expandHome(args[0])
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, target)
}

// resolveFile returns the absolute path of arg if it names a regular file.
func resolveFile(cwd string, arg string) string {
	if arg == nonLiteral {
		return ""
	}
	path := expandHome(arg)
	if !filepath.IsAbs(path) {
		if cwd == "" {
			return ""
		}
		path = filepath.Join(cwd, path)
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return filepath.Clean(path)
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// lineOption returns the count of head's -n N, -nN, -N, or --lines=N option.
func lineOption(args []string, fallback int) int {
	count := fallback
	for i, arg := range args {
		value := ""
		switch {
		case (arg == "-n" || arg == "--lines") && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(arg, "--lines="):
			value = strings.TrimPrefix(arg, "--lines=")
		case strings.HasPrefix(arg, "-n"):
			value = strings.TrimPrefix(arg, "-n")
		case len(arg) > 1 && arg[0] == '-':
			value = arg[1:]
		}
		if number, err := strconv.Atoi(value); err == nil {
			count = number
		}
	}
	return count
}

// tailOption returns tail's line count, or the first line of -n +K.
func tailOption(args []string) (int, int) {
	for i, arg := range args {
		value := ""
		switch {
		case (arg == "-n" || arg == "--lines") && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(arg, "--lines="):
			value = strings.TrimPrefix(arg, "--lines=")
		case strings.HasPrefix(arg, "-n") && len(arg) > 2:
			value = arg[2:]
		default:
			continue
		}
		if from, ok := strings.CutPrefix(value, "+"); ok {
			if number, err := strconv.Atoi(from); err == nil {
				return 0, number
			}
		}
		if number, err := strconv.Atoi(value); err == nil {
			return number, 0
		}
	}
	for _, arg := range args {
		if len(arg) > 1 && arg[0] == '-' {
			if number, err := strconv.Atoi(arg[1:]); err == nil {
				return number, 0
			}
		}
	}
	return 10, 0
}

var sedPrint = regexp.MustCompile(`^\s*(\d+|\$)\s*(?:,\s*(\d+|\$)\s*)?p\s*$`)

// sedPrintRanges returns the line ranges of sed -n scripts that only print
// lines by number, such as 'A,Bp' or '1,5p;10p'. Any other sed reads nothing.
func sedPrintRanges(args []string) ([][2]int, bool) {
	quiet := false
	var scripts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-n" || arg == "--quiet" || arg == "--silent":
			quiet = true
		case arg == "-e" && i+1 < len(args):
			scripts = append(scripts, args[i+1])
			i++
		case strings.HasPrefix(arg, "-i") || strings.HasPrefix(arg, "--in-place"):
			return nil, false
		case strings.HasPrefix(arg, "-"):
			// -E and other flags change nothing about printed line numbers.
		case len(scripts) == 0:
			scripts = append(scripts, arg)
		}
	}
	if !quiet || len(scripts) == 0 {
		return nil, false
	}
	var ranges [][2]int
	for _, script := range scripts {
		for _, part := range strings.Split(script, ";") {
			match := sedPrint.FindStringSubmatch(part)
			if match == nil {
				return nil, false
			}
			start, end := sedAddress(match[1]), sedAddress(match[2])
			if match[2] == "" {
				end = start
			}
			if start == 0 {
				// "$p" prints only the last line, which needs the line count.
				return nil, false
			}
			ranges = append(ranges, [2]int{start, end})
		}
	}
	return ranges, true
}

// sedAddress returns a line number, or 0 for "$", the last line.
func sedAddress(address string) int {
	number, _ := strconv.Atoi(address)
	return number
}

// shellCommands splits a Bash command into the words of its simple commands.
// It handles quotes, escapes, comments, the operators that separate commands,
// redirections, whose targets it drops, and heredocs, whose bodies it skips.
// A word with an expansion or a glob becomes nonLiteral.
func shellCommands(command string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	inWord, literal, dropNext := false, true, false
	var heredocs []string
	endWord := func() {
		if !inWord {
			return
		}
		text := word.String()
		if !literal {
			text = nonLiteral
		}
		if dropNext {
			dropNext = false
		} else {
			words = append(words, text)
		}
		word.Reset()
		inWord, literal = false, true
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			commands = append(commands, words)
		}
		words = nil
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\':
			if i+1 < len(runes) && runes[i+1] == '\n' {
				i++
				continue
			}
			if i+1 < len(runes) {
				i++
				word.WriteRune(runes[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			for i++; i < len(runes) && runes[i] != '\''; i++ {
				word.WriteRune(runes[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
				} else if runes[i] == '$' || runes[i] == '`' {
					literal = false
				}
				word.WriteRune(runes[i])
			}
		case c == '#' && !inWord:
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			i--
		case c == '\n':
			endCommand()
			// A heredoc body starts on the next line and ends at a line that
			// holds only its delimiter.
			for _, delimiter := range heredocs {
				for i+1 < len(runes) {
					end := i + 1
					for end < len(runes) && runes[end] != '\n' {
						end++
					}
					line := strings.TrimSpace(string(runes[i+1 : end]))
					i = end
					if line == delimiter {
						break
					}
				}
			}
			heredocs = nil
		case c == ' ' || c == '\t':
			endWord()
		case c == ';' || c == '|' || c == '&' || c == '(' || c == ')':
			endCommand()
		case c == '<' && i+1 < len(runes) && runes[i+1] == '<':
			endWord()
			i += 2
			if i < len(runes) && runes[i] == '<' {
				// A here-string: its word is the input, not a file.
				dropNext = true
				continue
			}
			if i < len(runes) && runes[i] == '-' {
				i++
			}
			for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
				i++
			}
			var delimiter strings.Builder
			for ; i < len(runes) && !strings.ContainsRune(" \t\n;|&<>()", runes[i]); i++ {
				if runes[i] != '\'' && runes[i] != '"' && runes[i] != '\\' {
					delimiter.WriteRune(runes[i])
				}
			}
			heredocs = append(heredocs, delimiter.String())
			i--
		case c == '>' || c == '<':
			// A file descriptor number before the operator is part of it.
			if inWord && literal && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			endWord()
			for i+1 < len(runes) && (runes[i+1] == '>' || runes[i+1] == '&') {
				i++
			}
			dropNext = true
		case c == '$' || c == '`' || c == '*' || c == '?' || c == '[':
			literal = false
			inWord = true
			word.WriteRune(c)
		default:
			inWord = true
			word.WriteRune(c)
		}
	}
	endCommand()
	return commands
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
