-- revert 0009
ALTER TABLE scoring_rules DROP CONSTRAINT IF EXISTS scoring_rules_operator_check;
ALTER TABLE scoring_rules ADD CONSTRAINT scoring_rules_operator_check
    CHECK (operator IN ('exists','equals','contains','gte','lte','between','in'));
