package migrate

import (
	"fmt"
	"strings"
)

// SplitStatements splits a PostgreSQL migration file into individual
// statements. Semicolons inside single-quoted strings, double-quoted
// identifiers, dollar-quoted bodies (DO $$ ... $$) and comments are ignored.
//
// The runner executes each statement separately inside one Go transaction, so
// files must not carry their own BEGIN/COMMIT; those control statements are
// dropped here to keep the whole file atomic.
func SplitStatements(input string) ([]string, error) {
	var (
		statements []string
		current    strings.Builder
		index      int
	)

	flush := func() {
		statement := strings.TrimSpace(current.String())
		current.Reset()
		if statement == "" || isTransactionControl(statement) {
			return
		}
		statements = append(statements, statement)
	}

	for index < len(input) {
		char := input[index]

		switch {
		case char == '-' && index+1 < len(input) && input[index+1] == '-':
			end := strings.IndexByte(input[index:], '\n')
			if end < 0 {
				current.WriteString(input[index:])
				index = len(input)
			} else {
				current.WriteString(input[index : index+end+1])
				index += end + 1
			}

		case char == '/' && index+1 < len(input) && input[index+1] == '*':
			end, err := blockCommentEnd(input, index)
			if err != nil {
				return nil, err
			}
			current.WriteString(input[index:end])
			index = end

		case char == '\'':
			end, err := singleQuotedEnd(input, index)
			if err != nil {
				return nil, err
			}
			current.WriteString(input[index:end])
			index = end

		case char == '"':
			end, err := doubleQuotedEnd(input, index)
			if err != nil {
				return nil, err
			}
			current.WriteString(input[index:end])
			index = end

		case char == '$':
			end, ok, err := dollarQuotedEnd(input, index)
			if err != nil {
				return nil, err
			}
			if ok {
				current.WriteString(input[index:end])
				index = end
				continue
			}
			current.WriteByte(char)
			index++

		case char == ';':
			flush()
			index++

		default:
			current.WriteByte(char)
			index++
		}
	}

	if strings.TrimSpace(current.String()) != "" {
		return nil, fmt.Errorf("%w: trailing content without semicolon", errStatementUnterminated)
	}
	return statements, nil
}

func isTransactionControl(statement string) bool {
	normalised := strings.ToUpper(strings.Join(strings.Fields(stripLeadingComments(statement)), " "))
	switch normalised {
	case "BEGIN", "BEGIN TRANSACTION", "COMMIT", "COMMIT TRANSACTION", "START TRANSACTION":
		return true
	default:
		return false
	}
}

func stripLeadingComments(statement string) string {
	trimmed := strings.TrimSpace(statement)
	for {
		if strings.HasPrefix(trimmed, "--") {
			if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
				trimmed = strings.TrimSpace(trimmed[newline+1:])
				continue
			}
			return ""
		}
		if strings.HasPrefix(trimmed, "/*") {
			end := strings.Index(trimmed, "*/")
			if end < 0 {
				return trimmed
			}
			trimmed = strings.TrimSpace(trimmed[end+2:])
			continue
		}
		return trimmed
	}
}

func blockCommentEnd(input string, start int) (int, error) {
	depth := 0
	for index := start; index < len(input)-1; index++ {
		switch input[index : index+2] {
		case "/*":
			depth++
			index++
		case "*/":
			depth--
			index++
			if depth == 0 {
				return index + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: unterminated block comment", errStatementUnterminated)
}

func singleQuotedEnd(input string, start int) (int, error) {
	for index := start + 1; index < len(input); index++ {
		if input[index] != '\'' {
			continue
		}
		if index+1 < len(input) && input[index+1] == '\'' {
			index++
			continue
		}
		return index + 1, nil
	}
	return 0, fmt.Errorf("%w: unterminated single-quoted string", errStatementUnterminated)
}

func doubleQuotedEnd(input string, start int) (int, error) {
	for index := start + 1; index < len(input); index++ {
		if input[index] != '"' {
			continue
		}
		if index+1 < len(input) && input[index+1] == '"' {
			index++
			continue
		}
		return index + 1, nil
	}
	return 0, fmt.Errorf("%w: unterminated quoted identifier", errStatementUnterminated)
}

func dollarQuotedEnd(input string, start int) (int, bool, error) {
	tagEnd := strings.IndexByte(input[start+1:], '$')
	if tagEnd < 0 {
		return 0, false, nil
	}
	tagEnd += start + 1
	tag := input[start : tagEnd+1]
	for _, char := range tag[1 : len(tag)-1] {
		if !(char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9') {
			return 0, false, nil
		}
	}
	end := strings.Index(input[tagEnd+1:], tag)
	if end < 0 {
		return 0, true, fmt.Errorf("%w: unterminated dollar quote %s", errStatementUnterminated, tag)
	}
	return tagEnd + 1 + end + len(tag), true, nil
}
