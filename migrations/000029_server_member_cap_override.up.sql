-- Per-server member cap override. NULL means inherit instance default.
ALTER TABLE servers ADD COLUMN member_cap_override INT;
