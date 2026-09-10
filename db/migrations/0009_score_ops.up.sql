-- widen scoring operator set (v2 rule engine)
ALTER TABLE scoring_rules DROP CONSTRAINT IF EXISTS scoring_rules_operator_check;
ALTER TABLE scoring_rules ADD CONSTRAINT scoring_rules_operator_check
    CHECK (operator IN ('exists','not_exists','equals','not_equals','contains','not_contains','gte','greater_than','lte','less_than','in','not_in','between'));
