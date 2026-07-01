-- =====================================================
-- 触发器模块增量 SQL
-- 执行方式: mysql -u root -p xclient < sql/triggers.sql
-- =====================================================

-- 触发器表（新增）
CREATE TABLE IF NOT EXISTS triggers (
    id VARCHAR(64) PRIMARY KEY COMMENT '触发器 ID',
    xclient_id VARCHAR(64) NOT NULL COMMENT '所属 X-Client 实例 ID',
    name VARCHAR(100) NOT NULL COMMENT '触发器名称',
    type VARCHAR(20) NOT NULL COMMENT '触发器类型: cron|once|interval|poll|webhook|on_message',
    config JSON NOT NULL COMMENT '触发器配置 JSON',
    reason TEXT COMMENT '触发原因描述',
    room_id VARCHAR(64) NOT NULL COMMENT '关联的聊天室 ID',
    room_valid BOOLEAN DEFAULT TRUE COMMENT '聊天室是否有效',
    status VARCHAR(20) DEFAULT 'enabled' COMMENT '状态: enabled|disabled|invalid|expired',
    invalid_reason VARCHAR(200) COMMENT '失效原因，如: room_deleted',
    last_fired_at BIGINT COMMENT '上次触发时间戳(毫秒)',
    fire_count INT DEFAULT 0 COMMENT '累计触发次数',
    max_fires INT COMMENT '最大触发次数，NULL=无限',
    cooldown_seconds INT DEFAULT 60 COMMENT '冷却时间(秒)',
    expires_at BIGINT COMMENT '过期时间戳(毫秒)',
    created_at BIGINT NOT NULL COMMENT '创建时间戳(毫秒)',
    updated_at BIGINT NOT NULL COMMENT '更新时间戳(毫秒)',
    UNIQUE KEY uk_xclient_trigger_name (xclient_id, name),
    INDEX idx_xclient_id (xclient_id),
    INDEX idx_room_id (room_id),
    INDEX idx_status (status),
    INDEX idx_type (type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='触发器表';

-- 触发器执行记录表（新增）
CREATE TABLE IF NOT EXISTS trigger_executions (
    id VARCHAR(64) PRIMARY KEY COMMENT '执行记录 ID',
    trigger_id VARCHAR(64) NOT NULL COMMENT '触发器 ID',
    fired_at BIGINT NOT NULL COMMENT '触发时间戳(毫秒)',
    status VARCHAR(20) DEFAULT 'pending' COMMENT '状态: pending|success|failed|skipped',
    error_message TEXT COMMENT '错误信息',
    execution_time_ms INT COMMENT '执行耗时(毫秒)',
    created_at BIGINT NOT NULL COMMENT '创建时间戳(毫秒)',
    INDEX idx_trigger_id (trigger_id),
    INDEX idx_fired_at (fired_at),
    FOREIGN KEY (trigger_id) REFERENCES triggers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='触发器执行记录表';

-- 轮询状态表（新增）
CREATE TABLE IF NOT EXISTS poll_states (
    trigger_id VARCHAR(64) PRIMARY KEY COMMENT '触发器 ID',
    `last_value` TEXT COMMENT '上次轮询到的值',
    last_checked_at BIGINT NOT NULL COMMENT '上次检查时间戳(毫秒)',
    FOREIGN KEY (trigger_id) REFERENCES triggers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='轮询状态表';

-- =====================================================
-- 测试数据（可选）
-- =====================================================

-- INSERT INTO triggers (id, xclient_id, name, type, config, reason, room_id, room_valid, status, fire_count, cooldown_seconds, created_at, updated_at)
-- VALUES ('trig_test_001', 'agent_001', '测试触发器', 'interval', '{"minutes": 5}', '每5分钟检查一次', 'room_default', TRUE, 'enabled', 0, 60, UNIX_MILLIS(), UNIX_MILLIS());
