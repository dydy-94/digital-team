-- =====================================================
-- Agent 关系感知与协作系统 - 数据库扩展
-- =====================================================

-- =====================================================
-- 1. Agent 关系表
-- =====================================================
CREATE TABLE IF NOT EXISTS agent_relations (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    agent_id VARCHAR(64) NOT NULL COMMENT 'Agent ID',
    relation_type VARCHAR(20) NOT NULL COMMENT '关系类型: colleague(同事)/superior(上级)/subordinate(下级)',
    related_agent_id VARCHAR(64) NOT NULL COMMENT '关联的 Agent ID',
    room_id VARCHAR(64) COMMENT '关联的聊天室（可选，为空表示全局关系）',
    description TEXT COMMENT '关系描述',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- 确保同一 Agent 的同一关系类型不会有重复关联
    UNIQUE KEY uk_agent_relation (agent_id, relation_type, related_agent_id),

    -- 索引
    INDEX idx_agent_id (agent_id),
    INDEX idx_room_id (room_id),
    INDEX idx_related_agent_id (related_agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Agent 关系表';

-- =====================================================
-- 2. 聊天室配置表
-- =====================================================
CREATE TABLE IF NOT EXISTS room_configs (
    room_id VARCHAR(64) PRIMARY KEY COMMENT '聊天室 ID',
    config JSON COMMENT '聊天室配置',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='聊天室配置表';

-- =====================================================
-- 3. agents 表扩展字段
-- =====================================================
-- 检查并添加新字段（如果不存在）
SET @exist := (SELECT COUNT(*) FROM information_schema.COLUMNS
               WHERE TABLE_SCHEMA = DATABASE()
               AND TABLE_NAME = 'agents'
               AND COLUMN_NAME = 'role');
SET @sqlstmt := IF(@exist = 0, 'ALTER TABLE agents ADD COLUMN role VARCHAR(100) COMMENT ''Agent 角色'' AFTER endpoint', 'SELECT ''Column already exists''');
PREPARE stmt FROM @sqlstmt;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @exist := (SELECT COUNT(*) FROM information_schema.COLUMNS
               WHERE TABLE_SCHEMA = DATABASE()
               AND TABLE_NAME = 'agents'
               AND COLUMN_NAME = 'description');
SET @sqlstmt := IF(@exist = 0, 'ALTER TABLE agents ADD COLUMN description TEXT COMMENT ''Agent 描述'' AFTER role', 'SELECT ''Column already exists''');
PREPARE stmt FROM @sqlstmt;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @exist := (SELECT COUNT(*) FROM information_schema.COLUMNS
               WHERE TABLE_SCHEMA = DATABASE()
               AND TABLE_NAME = 'agents'
               AND COLUMN_NAME = 'avatar');
SET @sqlstmt := IF(@exist = 0, 'ALTER TABLE agents ADD COLUMN avatar VARCHAR(255) COMMENT ''头像 URL'' AFTER description', 'SELECT ''Column already exists''');
PREPARE stmt FROM @sqlstmt;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- =====================================================
-- 验证
-- =====================================================
-- SHOW TABLES LIKE 'agent_%';
-- DESCRIBE agent_relations;
-- DESCRIBE room_configs;
-- DESCRIBE agents;
