package main

import (
	"fmt"
	"strings"
	"time"

	"wms/internal/audit"
)

func printReport(r audit.ProductReport) {
	fmt.Printf("Продукт %s\n", r.ProductID)
	for _, s := range r.Stages {
		printStage(s)
	}
}

func printStage(s audit.Stage) {
	line := []string{actionTypeName(s.ActionType), s.ActionID.String(), string(s.Status)}
	if s.BlockNumber > 0 {
		block := fmt.Sprintf("block %d", s.BlockNumber)
		if s.BlockTime != nil {
			block += " " + s.BlockTime.Format(time.RFC3339)
		}
		line = append(line, block)
	}
	if s.Operator != "" {
		line = append(line, fmt.Sprintf("оператор %s (%s)", s.Operator, s.OperatorID))
	}
	if s.OrgName != "" {
		line = append(line, "орг "+s.OrgName)
	}
	if s.RejectReason != 0 {
		line = append(line, fmt.Sprintf("причина %d", s.RejectReason))
	}
	line = append(line, fmt.Sprintf("детали %s  товары %s  ключ %s  подпись %s",
		checkMark(s.DetailsHash), checkMark(s.Items), checkMark(s.Signer), checkMark(s.Signature)))
	fmt.Println(strings.Join(line, "  "))
	for _, f := range s.Findings {
		fmt.Printf("  ✗ %s: %s\n", f.Kind, f.Detail)
	}
}

func printFindings(findings []audit.Finding) {
	for _, f := range findings {
		fmt.Printf("✗ %s: %s\n", f.Kind, f.Detail)
	}
	fmt.Printf("расхождений: %d\n", len(findings))
}

func checkMark(c audit.Check) string {
	switch c {
	case audit.CheckOK:
		return "✓"
	case audit.CheckMismatch:
		return "✗"
	default:
		return "—"
	}
}

func actionTypeName(t uint8) string {
	switch t {
	case 1:
		return "Receive"
	case 2:
		return "PutAway"
	case 3:
		return "Pick"
	case 4:
		return "Ship"
	default:
		return fmt.Sprintf("Type%d", t)
	}
}
