ALTER TABLE audit_log RENAME TO audit_log_partitioned;
ALTER TABLE audit_log_unpartitioned RENAME TO audit_log;

DROP TABLE audit_log_partitioned;
