package resetdatabases

import "fmt"

// PrintErrorBanner displays a noticeable error banner to stdout
func PrintErrorBanner(severity, title, description, expected, action string) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Printf("║  %s %s\n", severity, title)
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	if description != "" {
		fmt.Printf("║  What happened: %-47s ║\n", description)
	}
	if expected != "" {
		fmt.Printf("║  Expected: %-52s ║\n", expected)
	}
	if action != "" {
		fmt.Printf("║  Action: %-54s ║\n", action)
	}
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}
