-- ===============================================
-- x-client 多 Agent 协作系统 - 数据库表结构
-- 架构：无 Redis，纯 HTTP 轮询 + MySQL
-- ===============================================

-- 1. Agent 注册表
CREATE TABLE IF NOT EXISTS agents (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    agent_id VARCHAR(64) UNIQUE NOT NULL COMMENT 'Agent 唯一标识',
    endpoint VARCHAR(512) NOT NULL COMMENT 'Agent HTTP 访问地址',
    status VARCHAR(32) DEFAULT 'ONLINE' COMMENT 'ONLINE/OFFLINE',
    last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP COMMENT '最后心跳时间',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_agent_id (agent_id),
    INDEX idx_status_heartbeat (status, last_heartbeat)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 注册表';

-- 2. 聊天室表
CREATE TABLE IF NOT EXISTS rooms (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    room_id VARCHAR(64) UNIQUE NOT NULL COMMENT '聊天室唯一标识',
    name VARCHAR(128) NOT NULL COMMENT '聊天室名称',
    description TEXT COMMENT '聊天室描述',
    created_by VARCHAR(64) COMMENT '创建者',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_room_id (room_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天室表';

-- 3. 聊天室成员表
CREATE TABLE IF NOT EXISTS members (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    room_id VARCHAR(64) NOT NULL COMMENT '聊天室 ID',
    member_id VARCHAR(64) NOT NULL COMMENT '成员 ID（agent_id 或 user_id）',
    member_type VARCHAR(32) NOT NULL COMMENT 'agent / user',
    joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    left_at DATETIME NULL COMMENT '离开时间，NULL 表示仍在聊天室',
    is_active BOOLEAN DEFAULT TRUE COMMENT '是否活跃',

    UNIQUE KEY uk_room_member (room_id, member_id),
    INDEX idx_room_id (room_id),
    INDEX idx_member_id (member_id),
    INDEX idx_member_type (member_id, member_type),

    FOREIGN KEY (room_id) REFERENCES rooms(room_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天室成员表';

-- 4. 平台用户表
CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id VARCHAR(64) UNIQUE NOT NULL COMMENT '用户唯一标识',
    username VARCHAR(64) NOT NULL COMMENT '用户名',
    password_hash VARCHAR(256) COMMENT '密码哈希',
    email VARCHAR(128),
    avatar_url VARCHAR(512),
    status VARCHAR(32) DEFAULT 'OFFLINE' COMMENT 'ONLINE/OFFLINE',
    last_login DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_user_id (user_id),
    INDEX idx_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='平台用户表';

-- 5. 消息表（核心消息传递媒介）
CREATE TABLE IF NOT EXISTS messages (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    msg_id VARCHAR(64) UNIQUE NOT NULL COMMENT '消息唯一标识',
    room_id VARCHAR(64) NOT NULL COMMENT '聊天室 ID',

    -- 发送者信息
    sender_id VARCHAR(64) NOT NULL COMMENT '发送者 ID（agent_id 或 user_id）',
    sender_type VARCHAR(32) NOT NULL COMMENT 'agent / user / system',

    -- 接收者信息
    target_id VARCHAR(64) DEFAULT 'ALL' COMMENT '目标 ID，ALL 表示广播',
    target_type VARCHAR(32) DEFAULT 'BROADCAST' COMMENT 'BROADCAST / DIRECT',

    -- @ 提及的用户（JSON 数组）
    mention_users TEXT COMMENT 'JSON 数组，存放被 @ 的用户 ID 列表',

    -- 消息内容
    content TEXT NOT NULL COMMENT '消息内容',
    intent VARCHAR(32) DEFAULT 'INFORM' COMMENT 'INFORM / REQUEST / RESPONSE / SYSTEM',

    -- 消息状态（用于 poll 模式）
    status VARCHAR(32) DEFAULT 'PENDING' COMMENT 'PENDING / DELIVERED / READ',
    reply_to_msg_id VARCHAR(64) COMMENT '回复的消息 ID',

    -- 时间戳
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    delivered_at DATETIME NULL,
    read_at DATETIME NULL,

    INDEX idx_room_created (room_id, created_at DESC),
    INDEX idx_target_status (target_id, status, created_at DESC),
    INDEX idx_sender_created (sender_id, created_at DESC),
    INDEX idx_mention_users (mention_users(255)),
    INDEX idx_status_created (status, created_at),

    FOREIGN KEY (room_id) REFERENCES rooms(room_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息表';

-- 6. 消息投递记录表（追踪消息是否已被 poll 拉取和 WebSocket 通知）
CREATE TABLE IF NOT EXISTS message_delivery (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    msg_id VARCHAR(64) NOT NULL COMMENT '消息 ID',
    recipient_id VARCHAR(64) NOT NULL COMMENT '接收者 ID（agent_id 或 user_id）',
    delivered_at DATETIME DEFAULT CURRENT_TIMESTAMP COMMENT '已被 poll 拉取的时间',
    notified_at DATETIME NULL COMMENT '已通过 WebSocket 通知的时间',

    UNIQUE KEY uk_msg_recipient (msg_id, recipient_id),
    INDEX idx_recipient (recipient_id),
    INDEX idx_notified (recipient_id, notified_at),

    FOREIGN KEY (room_id) REFERENCES rooms(room_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息表';

-- 6.1 消息表扩展：增加 task_id 字段（支持消息与任务绑定）
ALTER TABLE messages ADD COLUMN task_id VARCHAR(64) NULL COMMENT '关联的任务 ID';
CREATE INDEX idx_messages_task_id ON messages(task_id);

-- 7. 任务表（Task）
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

-- 8. 任务关注点表（Focus Item）
CREATE TABLE IF NOT EXISTS focus_items (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    item_id VARCHAR(64) NOT NULL UNIQUE COMMENT '关注点唯一标识',
    task_id VARCHAR(64) NOT NULL COMMENT '关联的任务 ID',
    content TEXT NOT NULL COMMENT '关注点内容，如: "[ ] 设计 API 文档"',
    status VARCHAR(8) NOT NULL DEFAULT '[ ]' COMMENT '[ ] 未完成 / [/] 进行中 / [x] 已完成',
    agent_id VARCHAR(64) NOT NULL COMMENT '负责的 Agent',
    room_id VARCHAR(64) NOT NULL COMMENT '关联的聊天室',
    item_order INT NOT NULL DEFAULT 0 COMMENT '排序顺序',

    created_at BIGINT UNSIGNED NOT NULL COMMENT '创建时间戳',
    updated_at BIGINT UNSIGNED NOT NULL COMMENT '更新时间戳',

    INDEX idx_focus_task_id (task_id),
    INDEX idx_focus_agent_id (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='任务关注点表';

-- 9. Agent 权限表
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

-- 10. 文件传输记录表
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

-- ===============================================
-- 初始化数据
-- ===============================================

-- 插入一个测试聊天室
INSERT INTO rooms (room_id, name, description, created_by)
VALUES ('room_default', '默认聊天室', '系统默认聊天室', 'system')
ON DUPLICATE KEY UPDATE name = name;

-- 插入测试用户
INSERT INTO users (user_id, username, password_hash, email)
VALUES
    ('user_admin', 'admin', '$2a$10$...', 'admin@example.com'),
    ('user_test', 'testuser', '$2a$10$...', 'test@example.com')
ON DUPLICATE KEY UPDATE username = username;

-- ===============================================
-- 定期清理任务（可配置到业务逻辑中）
-- ===============================================

-- 清理 7 天前的已投递消息
-- DELETE FROM messages
-- WHERE status = 'DELIVERED'
--   AND created_at < DATE_SUB(NOW(), INTERVAL 7 DAY);

-- 清理过期的发言锁
-- DELETE FROM speaker_locks
-- WHERE expires_at < NOW();

-- 清理离线超过 30 分钟的 agent
-- UPDATE agents
-- SET status = 'OFFLINE'
-- WHERE status = 'ONLINE'
--   AND last_heartbeat < DATE_SUB(NOW(), INTERVAL 30 MINUTE);
