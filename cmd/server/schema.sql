CREATE TABLE IF NOT EXISTS expenses (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  amount NUMERIC(12,2) NOT NULL CHECK (amount > 0),
  category TEXT NOT NULL CHECK (category IN ('еда','транспорт','жильё','развлечения','здоровье','покупки','другое')),
  spent_at DATE NOT NULL,
  comment TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS expenses_user_spent_idx ON expenses(user_id, spent_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS expenses_category_idx ON expenses(category, spent_at DESC);
