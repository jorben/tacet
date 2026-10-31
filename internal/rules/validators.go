package rules

import "strings"

// ValidLuhn checks a payment-card style number. Spaces and hyphens between
// digit groups are ignored; any other non-digit makes the value invalid.
func ValidLuhn(text string) bool {
	sum, digits, double := 0, 0, false
	for i := len(text) - 1; i >= 0; i-- {
		c := text[i]
		if c == ' ' || c == '-' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
		n := int(c - '0')
		if double {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
		digits++
	}
	return digits >= 12 && digits <= 19 && sum%10 == 0
}

var idWeights = [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}

const idCheck = "10X98765432"

// ValidChineseID checks the 18-character resident identity number format and
// its ISO 7064 check character, and that the encoded birth date exists.
func ValidChineseID(text string) bool {
	text = strings.ToUpper(text)
	if len(text) != 18 {
		return false
	}
	sum := 0
	for i := 0; i < 17; i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
		sum += int(text[i]-'0') * idWeights[i]
	}
	if text[17] != idCheck[sum%11] {
		return false
	}
	year := int(text[6]-'0')*1000 + int(text[7]-'0')*100 + int(text[8]-'0')*10 + int(text[9]-'0')
	month := int(text[10]-'0')*10 + int(text[11]-'0')
	day := int(text[12]-'0')*10 + int(text[13]-'0')
	if year < 1900 || year > 2100 || month < 1 || month > 12 || day < 1 {
		return false
	}
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	limit := days[month-1]
	if month == 2 && (year%4 == 0 && year%100 != 0 || year%400 == 0) {
		limit = 29
	}
	return day <= limit
}
