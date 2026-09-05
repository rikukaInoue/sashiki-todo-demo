-- ベースラインのスキーマ + シードデータ。
-- twig サーバーではこれをベース mysqld に投入したうえで、
-- mysqld を正常終了させてから @init スナップショットを取得する(twig README 参照)。
-- ローカル開発では compose.yaml が initdb としてそのまま読み込む。

SET NAMES utf8mb4;

CREATE DATABASE IF NOT EXISTS todo;
USE todo;

CREATE TABLE IF NOT EXISTS todos (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  title      VARCHAR(255)    NOT NULL,
  done       TINYINT(1)      NOT NULL DEFAULT 0,
  created_at TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO todos (title, done) VALUES
  ('twig のベースラインに入っているシードデータ', 1),
  ('PR を開いて DB ブランチが生えるのを見る', 0),
  ('このブランチで好きに壊してみる(他のブランチは無傷)', 0);
