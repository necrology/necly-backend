-- Deliberately invalid migration used only by the W1 abort rehearsal.
-- The first statement is rolled back when the last statement fails.
BEGIN;
CREATE TABLE w1_abort_probe (id integer PRIMARY KEY);
INSERT INTO w1_abort_probe (id) VALUES (1);
SELECT * FROM w1_missing_table;
COMMIT;
