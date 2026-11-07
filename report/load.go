package report

import (
	"encoding/json"
	"fmt"
	"os"
)

func Load(reportPath string) (Report, error) {
	b, err := os.ReadFile(jsonPath(reportPath))
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return Report{}, fmt.Errorf("%s: %w", jsonPath(reportPath), err)
	}
	return r, nil
}
