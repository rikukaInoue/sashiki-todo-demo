-- TODO に優先度カラムを追加するマイグレーション。
-- この PR のブランチにだけ適用される(baseline と他 PR のスキーマは無傷)。
ALTER TABLE todos ADD COLUMN priority INT NOT NULL DEFAULT 0;
UPDATE todos SET priority = 2 WHERE title LIKE '%壊してみる%';
INSERT INTO todos (title, priority) VALUES ('優先度つきタスク(このPRの新スキーマ)', 3);
