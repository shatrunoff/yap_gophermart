package server

// isLuhnValid проверяет корректность номера по алгоритму Луна.
func isLuhnValid(number string) bool {
	if number == "" {
		return false
	}
	sum := 0
	alt := false
	for i := len(number) - 1; i >= 0; i-- {
		c := number[i]
		if c < '0' || c > '9' {
			return false
		}
		n := int(c - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}
