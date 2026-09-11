package main

import "fmt"

// const (
// 	parentheses1 = 40
// 	parentheses2 = 41

// 	squareBrackets1 = 91
// 	squareBrackets2 = 93

// 	curlyBrackets1 = 123
// 	curlyBrackets2 = 125
// )

func main() {

	s := "([}}])"
	s2 := "{()}"

	fmt.Println(isValid(s))
	fmt.Println(isValid(s2))
}

var hashMap = map[rune]rune{
	'}': '{',
	']': '[',
	')': '(',
}

func isValid(s string) bool {
	if len(s)%2 != 0 {
		return false
	}

	stack := []rune{}

	// ([}}])
	for _, v := range s {

		if beginBracket, ok := hashMap[v]; ok {

			if len(stack) == 0 {
				return false
			}

			if beginBracket == stack[len(stack)-1] {
				stack = stack[:len(stack)-1]
			} else {
				return false
			}

		} else {
			stack = append(stack, v)
		}
	}

	return len(stack) == 0

}
