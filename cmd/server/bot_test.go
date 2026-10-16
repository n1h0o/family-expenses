package main

import "testing"

func TestPurchaseCommentExtractsPurchaseName(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "amount in rubles", text: "кофе 350", want: "кофе"},
		{name: "amount with currency", text: "такси 1 200 ₽ вчера", want: "такси"},
		{name: "decimal amount", text: "покупка 350,50 руб сегодня", want: "покупка"},
		{name: "surrounding spaces", text: "  хлеб  120  ", want: "хлеб"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := purchaseComment(test.text); got != test.want {
				t.Errorf("purchaseComment(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestValidDateOrEmpty(t *testing.T) {
	tests := []struct {
		date string
		want bool
	}{
		{date: "", want: true},
		{date: "2026-10-16", want: true},
		{date: "2026-02-30", want: false},
		{date: "16-10-2026", want: false},
	}

	for _, test := range tests {
		if got := validDateOrEmpty(test.date); got != test.want {
			t.Errorf("validDateOrEmpty(%q) = %t, want %t", test.date, got, test.want)
		}
	}
}

func TestValidateExpenseRejectsInvalidFields(t *testing.T) {
	valid := ExpenseInput{
		UserID:   10,
		Amount:   350,
		Category: "еда",
		SpentAt:  "2026-10-16",
		Comment:  "кофе",
	}
	tests := []struct {
		name  string
		input ExpenseInput
	}{
		{name: "missing user", input: func() ExpenseInput { v := valid; v.UserID = 0; return v }()},
		{name: "nonpositive amount", input: func() ExpenseInput { v := valid; v.Amount = 0; return v }()},
		{name: "unknown category", input: func() ExpenseInput { v := valid; v.Category = "прочее"; return v }()},
		{name: "invalid date", input: func() ExpenseInput { v := valid; v.SpentAt = "2026-02-30"; return v }()},
		{name: "long comment", input: func() ExpenseInput { v := valid; v.Comment = string(make([]byte, 501)); return v }()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validate(test.input); err == nil {
				t.Error("validate() error = nil, want validation error")
			}
		})
	}

	if err := validate(valid); err != nil {
		t.Errorf("validate(valid) error = %v", err)
	}
}
