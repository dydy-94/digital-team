-- 新增 task_id 字段到 messages
ALTER TABLE messages ADD COLUMN task_id VARCHAR(64) NULL COMMENT '关联的任务 ID';
CREATE INDEX idx_messages_task_id ON messages(task_id);

-- 任务表（Task）
CREATE TABLE IF NOT EXISTS tasks (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    task_id VARCHAR(64) NOT NULL UNIQUE COMMENT '任务唯一标识',
    title TEXT NOT NULL COMMENT '任务标题',
    description TEXT COMMENT '任务描述',
    status VARCHAR(32) NOT NULL DEFAULT 'todo' COMMENT 'todo / in_progress / done',
    priority INT NOT NULL DEFAULT 3 COMMENT '优先级 1-5',
    created_by VARCHAR(64) NOT NULL COMMENT '创建者 agent_id',
    assigned_to VARCHAR(64) NOT NULL COMMENT '被分配的 agent_id',
    room_id VARCHAR(64) NOT NULL COMMENT '关联的聊天室',
    parent_task_id VARCHAR(64) COMMENT '父任务 ID（可选）',
    created_at BIGINT UNSIGNED NOT NULL COMMENT '创建时间戳',
    updated_at BIGINT UNSIGNED NOT NULL COMMENT '更新时间戳',
    completed_at BIGINT UNSIGNED COMMENT '完成时间戳',
    INDEX idx_tasks_room_id (room_id),
    INDEX idx_tasks_assigned_to (assigned_to),
    INDEX idx_tasks_created_by (created_by),
    INDEX idx_tasks_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='任务表';

-- 任务关注点表（Focus Item）
CREATE TABLE IF NOT EXISTS focus_items (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    item_id VARCHAR(64) NOT NULL UNIQUE COMMENT '关注点唯一标识',
    task_id VARCHAR(64) NOT NULL COMMENT '关联的任务 ID',
    content TEXT NOT NULL COMMENT '关注点内容',
    status VARCHAR(8) NOT NULL DEFAULT '[ ]' COMMENT '[ ] / [/] / [x]',
    agent_id VARCHAR(64) NOT NULL COMMENT '负责的 Agent',
    room_id VARCHAR(64) NOT NULL COMMENT '关联的聊天室',
    item_order INT NOT NULL DEFAULT 0 COMMENT '排序顺序',
    created_at BIGINT UNSIGNED NOT NULL COMMENT '创建时间戳',
    updated_at BIGINT UNSIGNED NOT NULL COMMENT '更新时间戳',
    INDEX idx_focus_task_id (task_id),
    INDEX idx_focus_agent_id (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='任务关注点表';

-- Agent 权限表
CREATE TABLE IF NOT EXISTS agent_permissions (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    agent_id VARCHAR(64) NOT NULL UNIQUE COMMENT 'Agent 唯一标识',
    level VARCHAR(8) NOT NULL DEFAULT 'l1' COMMENT '权限级别 l1/l2/l3',
    allowed_tools TEXT COMMENT '允许的工具列表，JSON 数组',
    denied_tools TEXT COMMENT '禁止的工具列表，JSON 数组',
    daily_token_limit BIGINT UNSIGNED COMMENT '每日 token 限额',
    monthly_token_limit BIGINT UNSIGNED COMMENT '每月 token 限额',
    file_size_limit_mb INT COMMENT '文件大小限制 MB',
    message_limit_per_hour INT COMMENT '每小时消息限制',
    created_at BIGINT UNSIGNED NOT NULL COMMENT '创建时间戳',
    updated_at BIGINT UNSIGNED NOT NULL COMMENT '更新时间戳',
    INDEX idx_perm_agent_id (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 权限表';

-- 文件传输记录表
CREATE TABLE IF NOT EXISTS file_transfers (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    transfer_id VARCHAR(64) NOT NULL UNIQUE COMMENT '传输唯一标识',
    file_name TEXT NOT NULL COMMENT '文件名',
    file_size BIGINT UNSIGNED NOT NULL COMMENT '文件大小 bytes',
    mime_type VARCHAR(128) COMMENT 'MIME 类型',
    from_agent VARCHAR(64) NOT NULL COMMENT '发送方 agent_id',
    to_agent VARCHAR(64) COMMENT '接收方 agent_id',
    room_id VARCHAR(64) NOT NULL COMMENT '关联的聊天室',
    task_id VARCHAR(64) COMMENT '关联的任务 ID（可选）',
    s3_key TEXT NOT NULL COMMENT 'S3 对象 key',
    status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT 'pending / uploading / completed / failed',
    created_at BIGINT UNSIGNED NOT NULL COMMENT '创建时间戳',
    completed_at BIGINT UNSIGNED COMMENT '完成时间戳',
    INDEX idx_transfer_room_id (room_id),
    INDEX idx_transfer_from_agent (from_agent),
    INDEX idx_transfer_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='文件传输记录表';
