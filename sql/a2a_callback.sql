-- =====================================================
-- A2A 回调功能增量 SQL
-- 执行方式: mysql -u root -p xclient < sql/a2a_callback.sql
-- =====================================================

-- 为 agents 表添加 callback_url 字段
-- 注意: MySQL 不支持 ADD COLUMN IF NOT EXISTS，使用存储过程实现幂等
DROP PROCEDURE IF EXISTS add_callback_url_column;
DELIMITER //
CREATE PROCEDURE add_callback_url_column()
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'agents'
          AND COLUMN_NAME = 'callback_url'
    ) THEN
        ALTER TABLE agents ADD COLUMN callback_url VARCHAR(500) DEFAULT '' COMMENT 'A2A 回调 URL' AFTER endpoint;
    END IF;
END //
DELIMITER ;
CALL add_callback_url_column();
DROP PROCEDURE IF EXISTS add_callback_url_column;
