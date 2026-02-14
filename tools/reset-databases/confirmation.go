package resetdatabases

import (
	"bufio"
	"errors"
	"fmt"
	stdos "os"
	"strings"
)

// PromptForConfirmation prompts user for two confirmation keys and validates them
// Returns error if validation fails or stdin I/O error occurs
func PromptForConfirmation(key1, key2 string) error {
	reader := bufio.NewReader(stdos.Stdin)

	fmt.Println("Input confirmation key 1?")
	userInputConfirmationKey1, err := reader.ReadString('\n')
	if err != nil {
		PrintErrorBanner(
			"❌ ERROR:",
			"Failed to Read Confirmation Key 1",
			fmt.Sprintf("stdin I/O error: %s", err.Error()),
			"",
			"Check if stdin is properly connected",
		)
		return err
	}
	userInputConfirmationKey1 = strings.TrimSpace(userInputConfirmationKey1)

	fmt.Println("Input the input confirmation key 2 to confirm:")
	userInputConfirmationKey2, err := reader.ReadString('\n')
	if err != nil {
		PrintErrorBanner(
			"❌ ERROR:",
			"Failed to Read Confirmation Key 2",
			fmt.Sprintf("stdin I/O error: %s", err.Error()),
			"",
			"Check if stdin is properly connected",
		)
		return err
	}
	userInputConfirmationKey2 = strings.TrimSpace(userInputConfirmationKey2)

	if userInputConfirmationKey1 != key1 {
		PrintErrorBanner(
			"❌ ERROR:",
			"Confirmation Key 1 Wrong",
			fmt.Sprintf("You entered: '%s' - this is wrong", userInputConfirmationKey1),
			"",
			"Re-run the tool and enter the correct confirmation key",
		)
		return errors.New("confirmation key 1 mismatch")
	}

	if userInputConfirmationKey2 != key2 {
		PrintErrorBanner(
			"❌ ERROR:",
			"Confirmation Key 2 Wrong",
			fmt.Sprintf("You entered: '%s' - this is wrong", userInputConfirmationKey2),
			"",
			"Re-run the tool and enter the correct confirmation key",
		)
		return errors.New("confirmation key 2 mismatch")
	}

	return nil
}
