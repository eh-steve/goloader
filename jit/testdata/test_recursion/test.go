package test_recursion

func tailRecursiveFibonacci(n, a, b uint64) uint64 {
	if n == 0 {
		return a
	}
	if n == 1 {
		return b
	}

	return tailRecursiveFibonacci(n-1, b, (a+b)%1000000007)
}

func Fib(n uint64) uint64 {
	return tailRecursiveFibonacci(n, 0, 1)
}
