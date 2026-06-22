class AgentTest {
    constructor() {
        this.ws = null;
        this.agents = [];
        this.isCoordinatorRunning = false;
        this.isAgentsRunning = false;
        this.basePort = 8000;
        this.currentChannelId = '';
        this.currentRoomAgents = [];
        this.rooms = [];
        this.messages = {};
        this.memberStatus = {}; // 存储成员在线状态 {channelId: {agentId: {online: bool}}}
        this.roomMembers = {}; // 存储聊天室成员列表 {channelId: [members]}
        this.memberStatusTimer = null; // 定时器

        // 用户相关
        this.currentUser = null; // {id, username, nickname}
        this.userId = null; // 用于 WebSocket 的 agentId

        window.app = this;

        this.init();
    }

    init() {
        this.bindEvents();
        this.renderAgentCards();
        this.connectToManager();
        this.loadStatus();
        // 启动成员状态定时检测（每10秒）
        this.startMemberStatusPolling();
    }
    
    // 启动成员状态定时检测（改为手动触发，不自动轮询）
    // 成员状态通过 WebSocket 消息推送来更新，只有收到其他人的消息时才检查
    // 如果需要强制刷新，可以调用 checkMemberStatus()
    startMemberStatusPolling() {
        // 不再自动轮询，交由 WebSocket 消息触发
        // this.memberStatusTimer = setInterval(() => {
        //     this.checkMemberStatus();
        // }, 10000);
    }
    
    // 停止成员状态检测
    stopMemberStatusPolling() {
        if (this.memberStatusTimer) {
            clearInterval(this.memberStatusTimer);
            this.memberStatusTimer = null;
        }
    }
    
    // 检测聊天室成员在线状态
    async checkMemberStatus(channelId = null) {
        const targetChannelId = channelId || this.currentChannelId;
        if (!targetChannelId) return;

        try {
            const res = await fetch(`/api/room/members?room_id=${encodeURIComponent(targetChannelId)}`);
            const data = await res.json();

            if (data.success && data.members) {
                // 转换字段名以匹配前端期望的格式
                const normalizedMembers = data.members.map(member => ({
                    agentId: member.member_id,
                    memberId: member.member_id,
                    memberType: member.member_type,
                    member_type: member.member_type,
                    // Agent 在线状态由 agents 表的 status 字段决定，user 由 members 表的 is_active 决定
                    online: member.member_type === 'agent' ? (member.agent_status === 'ONLINE') : member.is_active,
                    is_active: member.is_active,
                    agent_status: member.agent_status,
                    wsEstablished: member.ws_established,
                    joinedAt: member.joined_at
                }));

                // 更新成员状态
                this.memberStatus[targetChannelId] = {};
                this.roomMembers[targetChannelId] = normalizedMembers; // 缓存成员列表

                // 如果是当前聊天室，也更新 currentMembers
                if (targetChannelId === this.currentChannelId) {
                    this.currentMembers = normalizedMembers;
                }

                normalizedMembers.forEach(member => {
                    this.memberStatus[targetChannelId][member.agentId] = {
                        online: member.online
                    };
                });

                // 更新聊天室列表显示
                this.renderRooms();

                // 如果是当前聊天室，更新右侧成员面板
                if (targetChannelId === this.currentChannelId) {
                    this.renderMembersPanel();
                }
            }
        } catch (err) {
            console.error('检测成员状态失败:', err);
        }
    }

    // 加载所有聊天室的成员状态
    async loadAllRoomMembers() {
        for (const room of this.rooms) {
            await this.checkMemberStatus(room.id);
        }
    }

    // Tab 切换
    switchTab(tabName) {
        // 更新按钮状态
        document.querySelectorAll('.tab-btn').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.tab === tabName);
        });

        // 更新内容显示
        document.querySelectorAll('.tab-content').forEach(content => {
            content.classList.toggle('active', content.id === `${tabName}-list`);
        });
    }

    // 渲染成员面板（分别渲染 Agent 和 Human）
    renderMembersPanel() {
        const currentRoom = this.rooms.find(r => r.id === this.currentChannelId);
        
        if (!currentRoom) {
            document.getElementById('agents-list').innerHTML = '<p style="color: #999; text-align: center;">暂无 Agent</p>';
            document.getElementById('humans-list').innerHTML = '<p style="color: #999; text-align: center;">暂无成员</p>';
            return;
        }

        // 从成员状态中获取房间成员信息
        const members = this.roomMembers[this.currentChannelId] || [];

        // 分离 Agent 和 Human
        const agents = members.filter(member => member.memberType === 'agent');
        const humans = members.filter(member => member.memberType === 'user');

        // 渲染 Agent 列表
        const agentsContainer = document.getElementById('agents-list');
        if (agents.length === 0) {
            agentsContainer.innerHTML = '<p style="color: #999; text-align: center;">暂无 Agent</p>';
        } else {
            agentsContainer.innerHTML = agents.map(member => {
                const status = this.memberStatus[this.currentChannelId]?.[member.agentId];
                const isOnline = status ? status.online : false;
                const statusClass = isOnline ? 'online' : 'offline';
                
                return `
                    <div class="member-item">
                        <div class="member-avatar agent">A</div>
                        <div class="member-info">
                            <div class="member-name">${member.agentId}</div>
                            <div class="member-status ${statusClass}">
                                <span class="member-status-dot ${statusClass}"></span>
                                ${isOnline ? '在线' : '离线'}
                            </div>
                        </div>
                    </div>
                `;
            }).join('');
        }

        // 渲染 Human 列表
        const humansContainer = document.getElementById('humans-list');
        if (humans.length === 0) {
            humansContainer.innerHTML = '<p style="color: #999; text-align: center;">暂无成员</p>';
        } else {
            humansContainer.innerHTML = humans.map(member => {
                // 生成可读的显示名称
                let displayName = member.agentId;
                if (member.nickname && member.username) {
                    // 如果接口返回了昵称和用户名信息，显示格式为"昵称（用户名）"
                    displayName = `${member.nickname}（${member.username}）`;
                } else if (displayName.startsWith('user_')) {
                    // 如果是当前用户，显示其昵称或用户名
                    if (displayName === this.userId) {
                        displayName = this.currentUser.nickname || this.currentUser.username;
                    } else {
                        // 对于其他用户，显示用户ID
                        const userId = displayName.split('_')[1];
                        displayName = `用户 ${userId}`;
                    }
                }

                const status = this.memberStatus[this.currentChannelId]?.[member.agentId];
                const isOnline = status ? status.online : false;
                const statusClass = isOnline ? 'online' : 'offline';

                return `
                    <div class="member-item">
                        <div class="member-avatar human">H</div>
                        <div class="member-info">
                            <div class="member-name">${displayName}</div>
                            <div class="member-status ${statusClass}">
                                <span class="member-status-dot ${statusClass}"></span>
                                ${isOnline ? '在线' : '离线'}
                            </div>
                        </div>
                    </div>
                `;
            }).join('');
        }
    }
    
    bindEvents() {
        // 页面刷新或关闭事件
        window.addEventListener('beforeunload', (e) => {
            if (this.ws) {
                this.log('info', '页面即将刷新/关闭，正在断开 WebSocket 连接...');
                this.disconnectWebSocket();
            }
        });

        // 用户登录/注册事件
        document.getElementById('login-btn').addEventListener('click', () => this.login());
        document.getElementById('register-btn').addEventListener('click', () => this.register());
        document.getElementById('to-register-btn').addEventListener('click', () => this.showRegisterForm());
        document.getElementById('to-login-btn').addEventListener('click', () => this.showLoginForm());
        document.getElementById('logout-btn').addEventListener('click', () => this.logout());
        document.getElementById('password-input').addEventListener('keypress', (e) => {
            if (e.key === 'Enter') this.login();
        });
        document.getElementById('nickname-input').addEventListener('keypress', (e) => {
            if (e.key === 'Enter') this.register();
        });

        // 抽屉事件
        document.getElementById('config-toggle').addEventListener('click', () => this.openDrawer());
        document.getElementById('close-drawer').addEventListener('click', () => this.closeDrawer());
        document.getElementById('config-overlay').addEventListener('click', () => this.closeDrawer());

        // 抽屉内的服务控制
        document.getElementById('drawer-start-coordinator').addEventListener('click', () => this.startCoordinator());
        document.getElementById('drawer-stop-coordinator').addEventListener('click', () => this.stopCoordinator());
        document.getElementById('drawer-start-agents').addEventListener('click', () => this.startAgents());
        document.getElementById('drawer-stop-agents').addEventListener('click', () => this.stopAgents());
        document.getElementById('drawer-agent-count').addEventListener('change', () => this.onDrawerAgentCountChange());

        // 聊天室相关
        document.getElementById('send-btn').addEventListener('click', () => {
            console.log('send-btn clicked');
            this.sendMessage();
        });
        document.getElementById('message-input').addEventListener('keypress', (e) => {
            console.log('message-input keypress:', e.key);
            if (e.key === 'Enter') this.sendMessage();
        });
        document.getElementById('sender-mode').addEventListener('change', (e) => this.onSenderModeChange(e));
        document.getElementById('create-room-btn').addEventListener('click', () => this.createRoom());
        document.getElementById('room-name-input').addEventListener('input', () => this.updateCreateRoomButton());

        // 聊天室加入/退出事件
        document.getElementById('leave-room-btn').addEventListener('click', () => this.leaveRoom());

        // Tab 切换事件
        document.querySelectorAll('.tab-btn').forEach(btn => {
            btn.addEventListener('click', (e) => this.switchTab(e.target.dataset.tab));
        });
    }

    // ========== 抽屉控制 ==========

    openDrawer() {
        document.getElementById('config-drawer').classList.add('open');
        document.getElementById('config-overlay').classList.add('show');
        document.getElementById('config-overlay').style.display = 'block';
    }

    closeDrawer() {
        document.getElementById('config-drawer').classList.remove('open');
        document.getElementById('config-overlay').classList.remove('show');
        setTimeout(() => {
            document.getElementById('config-overlay').style.display = 'none';
        }, 300);
    }

    onDrawerAgentCountChange() {
        const count = document.getElementById('drawer-agent-count').value;
        document.getElementById('agent-count').value = count;
        this.renderAgentCards();
    }
    
    connectToManager() {
        fetch('/api/health').then(res => {
            if (res.ok) {
                this.log('info', '管理服务已连接');
            }
        }).catch(() => {
            this.log('error', '管理服务未启动，请先运行 manager 服务');
        });
    }
    
    async loadStatus() {
        try {
            const res = await fetch('/api/status');
            const data = await res.json();
            this.log('info', `状态加载: coordinator=${data.coordinatorRunning}, agents=${data.agents?.length || 0}`);

            this.isCoordinatorRunning = data.coordinatorRunning;
            this.isAgentsRunning = data.agentsRunning;
            this.agents = data.agents || [];

            this.updateCoordinatorButtons();
            this.updateAgentsButtons();

            if (this.isCoordinatorRunning) {
                this.updateStatus('coordinator-status', 'running', '运行中');
                // 立即加载聊天室列表（会自动选中第一个并建立WS连接）
                await this.loadRooms();
                // 加载可用的 Agent 列表
                await this.loadAgents();
            } else if (this.agents.length > 0) {
                // 协调器未运行但有 agents 信息
                document.getElementById('agent-count').value = this.agents.length;
                document.getElementById('drawer-agent-count').value = this.agents.length;
                this.renderAgentCards();
                this.updateAgentCards();
                this.updateAgentSenderSelect();
                this.updateRoomAgentsSelect();
                const sendBtn = document.getElementById('send-btn');
                if (sendBtn) sendBtn.disabled = false;
            }
        } catch (err) {
            this.log('error', '加载状态失败: ' + err.message);
            setTimeout(() => {
                if (this.currentUser) {
                    this.connectWebSocket();
                }
            }, 500);
        }
    }

    async loadAgents() {
        if (!this.isCoordinatorRunning) {
            return;
        }
        
        try {
            const res = await fetch('/api/agents');
            const data = await res.json();
            
            if (data.success && data.agents) {
                // 转换为与管理服务返回格式兼容的数据结构
                this.agents = data.agents.map(agent => ({
                    id: agent.agent_id,
                    xclientPort: null, // 协调器API不返回端口信息
                    agentcorePort: null
                }));
                
                this.updateRoomAgentsSelect();
                this.log('info', `加载到 ${data.agents.length} 个可用的 Agent`);
            } else {
                this.log('warn', '未加载到可用的 Agent');
                this.agents = [];
                this.updateRoomAgentsSelect();
            }
        } catch (err) {
            this.log('error', '加载 Agent 列表失败: ' + err.message);
            this.agents = [];
            this.updateRoomAgentsSelect();
        }
    }
    
    async startCoordinator() {
        this.log('info', '正在启动协调器...');
        try {
            const res = await fetch('/api/coordinator/start', { method: 'POST' });
            const data = await res.json();
            if (data.success) {
                this.isCoordinatorRunning = true;
                this.updateCoordinatorButtons();
                this.updateStatus('coordinator-status', 'running', '运行中');
                this.updateStatus('drawer-coordinator-status', 'running', '运行中');
                this.log('success', '协调器启动成功');
                this.enableStartAgents();
                setTimeout(() => {
                    if (this.currentUser) {
                        this.connectWebSocket();
                    }
                }, 500);
            } else {
                this.log('error', '协调器启动失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '启动协调器时出错: ' + err.message);
        }
    }

    async stopCoordinator() {
        this.log('info', '正在停止协调器...');
        try {
            const res = await fetch('/api/coordinator/stop', { method: 'POST' });
            const data = await res.json();
            if (data.success) {
                this.isCoordinatorRunning = false;
                this.updateCoordinatorButtons();
                this.updateStatus('coordinator-status', 'stopped', '已停止');
                this.updateStatus('drawer-coordinator-status', 'stopped', '已停止');
                this.log('success', '协调器停止成功');
                this.disableStartAgents();
                this.disconnectWebSocket();
            } else {
                this.log('error', '协调器停止失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '停止协调器时出错: ' + err.message);
        }
    }

    // ========== 用户认证 ==========

    showRegisterForm() {
        document.getElementById('login-title').textContent = '注册账号';
        document.getElementById('login-form').style.display = 'none';
        document.getElementById('register-form').style.display = 'flex';
        document.getElementById('login-btn').style.display = 'none';
        document.getElementById('to-register-btn').style.display = 'none';
        document.getElementById('register-btn').style.display = 'block';
        document.getElementById('to-login-btn').style.display = 'block';
        document.getElementById('login-error').textContent = '';
        // 清空注册表单
        document.getElementById('reg-username-input').value = '';
        document.getElementById('reg-password-input').value = '';
        document.getElementById('nickname-input').value = '';
    }

    showLoginForm() {
        document.getElementById('login-title').textContent = '登录';
        document.getElementById('login-form').style.display = 'flex';
        document.getElementById('register-form').style.display = 'none';
        document.getElementById('login-btn').style.display = 'block';
        document.getElementById('to-register-btn').style.display = 'block';
        document.getElementById('register-btn').style.display = 'none';
        document.getElementById('to-login-btn').style.display = 'none';
        document.getElementById('login-error').textContent = '';
    }

    showLoginError(message) {
        document.getElementById('login-error').textContent = message;
    }

    async register() {
        const username = document.getElementById('reg-username-input').value.trim();
        const password = document.getElementById('reg-password-input').value;
        const nickname = document.getElementById('nickname-input').value.trim();

        if (!username || !password) {
            this.showLoginError('用户名和密码不能为空');
            return;
        }

        try {
            const res = await fetch('/api/user/register', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, password, nickname })
            });
            const data = await res.json();

            if (data.success) {
                // 清空注册表单
                document.getElementById('reg-username-input').value = '';
                document.getElementById('reg-password-input').value = '';
                document.getElementById('nickname-input').value = '';
                // 自动切换回登录表单
                this.showLoginForm();
                this.showLoginError('');
                // 填充用户名，方便登录
                document.getElementById('username-input').value = username;
                alert('注册成功！请登录');
            } else {
                this.showLoginError(data.error || '注册失败');
            }
        } catch (err) {
            this.showLoginError('注册出错: ' + err.message);
        }
    }

    async login() {
        const username = document.getElementById('username-input').value.trim();
        const password = document.getElementById('password-input').value;

        if (!username || !password) {
            this.showLoginError('用户名和密码不能为空');
            return;
        }

        try {
            const res = await fetch('/api/user/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, password })
            });
            const data = await res.json();

            if (data.success) {
                this.currentUser = data.user;
                this.userId = data.user.username; // WebSocket 中使用的 ID，直接使用用户名
                this.updateUserUI();
                this.showLoginError('');

                // 加载聊天室（会自动选中第一个并通过 joinRoom 建立WS连接）
                // 加载可用 agent 列表
                this.loadRooms();
                this.loadAgents();
            } else {
                this.showLoginError(data.error || '登录失败');
            }
        } catch (err) {
            this.showLoginError('登录出错: ' + err.message);
        }
    }

    logout() {
        this.currentUser = null;
        this.userId = null;
        this.updateUserUI();

        // 断开 WebSocket
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }

        // 清空聊天状态
        this.currentChannelId = '';
        this.currentRoomAgents = [];
        document.getElementById('agents-panel').style.display = 'none';
        document.getElementById('chat-panel').style.display = 'none';
        document.getElementById('room-actions').style.display = 'none';
        this.clearMessages();

        this.log('info', '已退出登录');
    }

    updateUserUI() {
        const loginPage = document.getElementById('login-page');
        const mainPage = document.getElementById('main-page');
        const userNickname = document.getElementById('user-nickname');

        if (this.currentUser) {
            // 切换到主页面
            loginPage.style.display = 'none';
            mainPage.style.display = 'block';
            userNickname.textContent = this.currentUser.nickname || this.currentUser.username;
        } else {
            // 切换到登录页面
            loginPage.style.display = 'flex';
            mainPage.style.display = 'none';
            // 清空输入框
            document.getElementById('username-input').value = '';
            document.getElementById('password-input').value = '';
            document.getElementById('reg-username-input').value = '';
            document.getElementById('reg-password-input').value = '';
            document.getElementById('nickname-input').value = '';
            // 重置为登录表单
            this.showLoginForm();
        }
    }

    // ========== Agent 管理 ==========

    async startAgents() {
        // 从抽屉内的选择器获取数量（因为主页面可能没有这个元素）
        const drawerAgentCount = document.getElementById('drawer-agent-count');
        const mainAgentCount = document.getElementById('agent-count');
        const count = parseInt(drawerAgentCount?.value || mainAgentCount?.value || '3');
        this.log('info', `正在启动 ${count} 个 Agent...`);

        try {
            const res = await fetch('/api/agents/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ count })
            });
            const data = await res.json();
            if (data.success) {
                this.isAgentsRunning = true;
                this.agents = data.agents;
                this.updateAgentsButtons();
                this.updateAgentCards();
                this.updateAgentSenderSelect();
                this.updateRoomAgentsSelect();
                this.log('success', `${count} 个 Agent 启动成功`);
                const sendBtn1 = document.getElementById('send-btn');
                if (sendBtn1) sendBtn1.disabled = false;

                // Agent 启动成功后，延迟检测成员在线状态（等待 Agent 完全启动并连接）
                if (this.currentChannelId) {
                    setTimeout(() => this.checkMemberStatus(), 2000);
                }
            } else {
                this.log('error', 'Agent 启动失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '启动 Agent 时出错: ' + err.message);
        }
    }

    async stopAgents() {
        this.log('info', '正在停止所有 Agent...');
        try {
            const res = await fetch('/api/agents/stop', { method: 'POST' });
            const data = await res.json();
            if (data.success) {
                this.isAgentsRunning = false;
                this.agents = [];
                this.updateAgentsButtons();
                this.updateAgentCards();
                this.updateAgentSenderSelect();
                this.updateRoomAgentsSelect();
                this.log('success', '所有 Agent 停止成功');
                const sendBtn2 = document.getElementById('send-btn');
                if (sendBtn2) sendBtn2.disabled = true;

                // Agent 停止后，立即检测成员在线状态
                if (this.currentChannelId) {
                    setTimeout(() => this.checkMemberStatus(), 100);
                }
            } else {
                this.log('error', 'Agent 停止失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '停止 Agent 时出错: ' + err.message);
        }
    }
    connectWebSocket() {
        if (this.ws) return;
        if (!this.currentUser) {
            this.log('warn', '请先登录');
            return;
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        let wsUrl = protocol + '//' + window.location.host + '/ws/chat?user_id=' + encodeURIComponent(this.currentUser.id);
        if (this.currentSessionId) {
            wsUrl += '&session_id=' + encodeURIComponent(this.currentSessionId);
        }
        if (this.currentChannelId) {
            wsUrl += '&room_id=' + encodeURIComponent(this.currentChannelId);
        }
        console.log('[WS] Connecting to:', wsUrl);
        this.ws = new WebSocket(wsUrl);

        this.ws.onopen = () => {
            this.log('success', 'WebSocket 连接成功');
            // 用户加入聊天室
            if (this.currentChannelId) {
                this.sendJoin();
            }
            // 注意：不在这里调用 checkMemberStatus，由 joinRoom 统一调用
        };
        
        this.ws.onmessage = (event) => {
            const data = JSON.parse(event.data);
            this.handleMessage(data);
        };
        
        this.ws.onerror = (event) => {
            let errorMsg = '未知错误';
            if (event && event.type === 'error') {
                errorMsg = '连接错误';
            }
            if (event && event.target && event.target.readyState === 3) {
                errorMsg = '连接已关闭';
            }
            this.log('error', 'WebSocket 连接错误: ' + errorMsg);
        };
        
        this.ws.onclose = (event) => {
            this.log('info', 'WebSocket 连接关闭');
            this.ws = null;
            // 如果是因 ws_established 冲突导致的关闭，清除 session_id
            // 前端离线状态由后端通过心跳超时检测
        };
    }

    async leaveRoom() {
        if (!this.currentChannelId) {
            return;
        }
        if (!this.currentUser) {
            return;
        }

        try {
            const res = await fetch('/api/room/leave', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    room_id: this.currentChannelId,
                    member_id: this.currentUser.id
                })
            });
            // ignore result
        } catch (err) {
            this.log('error', '离开聊天室时出错: ' + err.message);
        }
    }
    
    disconnectWebSocket() {
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }
    }
    
    sendJoin(channelId = this.currentChannelId) {
        if (!this.userId) {
            this.log('warn', '请先登录');
            return;
        }
        const msg = {
            action: 'join',
            data: {
                channelId: channelId,
                agentId: this.userId
            }
        };
        this.ws.send(JSON.stringify(msg));
    }
    
    sendMessage(forceRetry = false) {
        // 防止重复发送
        if (this.isSending) {
            this.log('info', '正在发送消息中，忽略重复调用');
            return;
        }

        // 检查是否已成功加入聊天室（必须有 session_id）
        if (!this.currentSessionId || !this.currentChannelId) {
            this.log('warn', '请先加入聊天室');
            return;
        }

        this.isSending = true;

        this.log('info', 'sendMessage called, ws state:', this.ws ? this.ws.readyState : 'null');
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
            this.log('error', 'WebSocket 未连接，状态: ' + (this.ws ? this.ws.readyState : 'null'));
            if (forceRetry) {
                this.log('error', 'WebSocket 连接失败，请检查协调器是否正在运行');
                this.isSending = false;
                return;
            }
            this.log('info', 'WebSocket 未连接，正在尝试重新连接...');
            this.connectWebSocket();
            setTimeout(() => {
                this.isSending = false;
                this.sendMessage(true);
            }, 1000);
            return;
        }

        const input = document.getElementById('message-input');
        const content = input.value.trim();
        this.log('info', 'content:', content);
        if (!content) {
            this.log('info', 'content is empty, returning');
            return;
        }

        // 获取发送者模式
        const senderMode = document.getElementById('sender-mode').value;
        let sender = this.userId; // 使用登录用户的 ID
        this.log('info', 'senderMode:', senderMode, 'sender:', sender);

        if (senderMode === 'agent') {
            const agentSender = document.getElementById('agent-sender').value;
            this.log('info', 'agentSender:', agentSender);
            if (!agentSender) {
                this.log('error', '请先选择一个 Agent 身份');
                return;
            }
            sender = agentSender;
        }

        if (!sender) {
            this.log('error', '请先登录');
            return;
        }

        const msg = {
            action: 'speak',
            data: {
                msgId: sender + '_' + Date.now(),
                channelId: this.currentChannelId,
                sender: sender,
                target: 'ALL',
                intent: 'INFORM',
                contentText: content,
                mentionUsers: this.extractMentions(content)
            }
        };

        this.log('info', '发送消息: ' + JSON.stringify(msg));
        // 添加乐观更新，立即显示用户发送的消息
        this.addMessage(sender, content);
        this.ws.send(JSON.stringify(msg));
        input.value = '';
        this.isSending = false;
    }
    
    extractMentions(content) {
        const regex = /@([a-zA-Z0-9_]+)/g;
        const mentions = [];
        let match;
        try {
            while ((match = regex.exec(content)) !== null) {
                mentions.push(match[1]);
            }
        } catch (e) {
            console.error('extractMentions error:', e);
        }
        console.log('extractMentions:', content, '->', mentions);
        return mentions;
    }
    
    handleMessage(data) {
        // 只处理消息，不主动查询成员状态
        // 成员状态由后端通过 WebSocket 定期推送（member_status 类型）
        if (data.type === 'message') {
            const msg = data.data;
            if (msg.channelId === this.currentChannelId) {
                // 过滤掉用户自己发送的消息，因为已经通过乐观更新显示过了
                if (msg.sender === this.currentUser.username) {
                    this.log('info', '忽略自己发送的消息的广播');
                    return;
                }
                this.addMessage(msg.sender, msg.contentText);
            }
        } else if (data.type === 'member_status') {
            // 后端主动推送的成员在线状态
            const msg = data.data;
            if (msg.roomId === this.currentChannelId) {
                // 更新成员状态（适配后端驼峰命名）
                const members = msg.members || [];
                const normalizedMembers = members.map(member => ({
                    agentId: member.memberId,
                    memberId: member.memberId,
                    memberType: member.memberType,
                    member_type: member.memberType,
                    // Agent 在线状态由 agents 表的 status 字段决定，user 由 members 表的 is_active 决定
                    online: member.memberType === 'agent' ? (member.agentStatus === 'ONLINE') : member.isActive,
                    is_active: member.isActive,
                    agent_status: member.agentStatus,
                    wsEstablished: member.wsEstablished,
                }));

                // 更新缓存
                this.roomMembers[msg.roomId] = normalizedMembers;
                this.memberStatus[msg.roomId] = {};
                normalizedMembers.forEach(member => {
                    this.memberStatus[msg.roomId][member.agentId] = {
                        online: member.online
                    };
                });

                // 更新当前聊天室成员
                if (msg.roomId === this.currentChannelId) {
                    this.currentMembers = normalizedMembers;
                    this.renderMembersPanel();
                }

                // 更新聊天室列表显示
                this.renderRooms();
            }
        } else if (data.type === 'warning' || data.type === 'error' || data.type === 'info') {
            // 系统通知消息（warning/error/info）
            const msg = data.data;
            if (msg.roomId === this.currentChannelId || !msg.roomId) {
                // 作为系统消息展示，警告消息显示为黄色
                this.addMessage('[系统通知]', msg.content, data.type === 'warning');
            }
            // 如果是会话验证失败/无效的 session_id 警告，清除 session_id
            if (data.type === 'warning' && msg.content && 
                (msg.content.includes('会话验证失败') || msg.content.includes('无效的 session_id'))) {
                this.log('warn', '收到会话冲突警告，清除 session_id，请重新加入聊天室');
                this.currentSessionId = null;
            }
        } else if (data.type === 'stream') {
            // 流式消息更新
            const msg = data.data;
            if (msg.channelId === this.currentChannelId) {
                this.updateStreamMessage(msg);
            }
        } else if (data.type === 'stream_complete') {
            // 流式消息完成
            const msg = data.data;
            if (msg.channelId === this.currentChannelId) {
                this.completeStreamMessage(msg);
            }
        }
        // 注意：history 消息不再通过 WebSocket 获取，改用 REST API /api/room/history
    }
    
    // 更新流式消息
    updateStreamMessage(msg) {
        let messageItem = document.getElementById(`msg-${msg.msgId}`);

        if (!messageItem) {
            // 如果消息不存在，创建新消息
            messageItem = this.createStreamMessage(msg);
        }

        // 更新消息内容
        const contentDiv = messageItem.querySelector('.message-content');
        contentDiv.innerHTML = this.escapeHtml(msg.contentText);

        // 添加加载动画（除非已经完成）
        if (msg.status === 'thinking') {
            contentDiv.innerHTML += '<span class="typing-indicator"><span>.</span><span>.</span><span>.</span></span>';
        } else if (msg.status === 'streaming') {
            contentDiv.innerHTML += '<span class="typing-cursor">|</span>';
        }

        // 滚动到底部
        const list = document.getElementById('message-list');
        list.scrollTop = list.scrollHeight;
    }
    
    // 完成流式消息
    completeStreamMessage(msg) {
        let messageItem = document.getElementById(`msg-${msg.msgId}`);

        if (!messageItem) {
            messageItem = this.createStreamMessage(msg);
        }

        // 更新消息内容（移除 loading 动画）
        const contentDiv = messageItem.querySelector('.message-content');
        contentDiv.innerHTML = this.escapeHtml(msg.contentText);

        // 移除完成状态标记
        messageItem.classList.remove('message-streaming');
        messageItem.classList.add('message-completed');

        // 滚动到底部
        const list = document.getElementById('message-list');
        list.scrollTop = list.scrollHeight;
    }
    
    // 创建流式消息元素
    createStreamMessage(msg) {
        const list = document.getElementById('message-list');
        const item = document.createElement('div');
        item.id = `msg-${msg.msgId}`;
        item.className = `message-item message-streaming`;
        
        const now = new Date();
        const timeStr = now.toLocaleTimeString('zh-CN');
        
        item.innerHTML = `
            <div class="message-sender">${this.escapeHtml(msg.sender)}</div>
            <div class="message-content"></div>
            <div class="message-time">${timeStr}</div>
        `;
        
        list.appendChild(item);
        return item;
    }
    
    // HTML 转义
    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
    
    addMessage(sender, content, timestamp = null, isWarning = false) {
        const list = document.getElementById('message-list');
        const item = document.createElement('div');
        item.className = 'message-item';
        if (isWarning) {
            item.classList.add('warning');
        }
        
        // 如果没有提供时间戳，使用当前时间
        let msgTime;
        if (timestamp && (timestamp instanceof Date || typeof timestamp === 'number')) {
            msgTime = timestamp instanceof Date ? timestamp : new Date(timestamp);
        } else {
            msgTime = new Date();
        }
        const timeStr = msgTime.toLocaleTimeString('zh-CN');

        // 生成可读的发送者名称
        let displaySender = sender;
        if (sender.startsWith('user_')) {
            // 如果是当前用户，显示昵称
            if (this.currentUser && sender === `user_${this.currentUser.id}`) {
                displaySender = this.currentUser.nickname || this.currentUser.username;
            } else {
                displaySender = sender; // 其他用户只显示 ID
            }
        } else if (this.currentUser && sender === this.currentUser.username) {
            // 如果是当前用户，显示昵称
            displaySender = this.currentUser.nickname || this.currentUser.username;
        }
        
        item.innerHTML = `
            <div class="message-sender">${displaySender}</div>
            <div class="message-content">${content}</div>
            <div class="message-time">${timeStr}</div>
        `;
        
        list.appendChild(item);
        list.scrollTop = list.scrollHeight;
    }
    
    log(type, message) {
        const list = document.getElementById('log-list');
        const item = document.createElement('div');
        item.className = `log-item ${type}`;
        
        const now = new Date();
        const timeStr = now.toLocaleTimeString('zh-CN');
        
        item.textContent = `[${timeStr}] ${message}`;
        list.appendChild(item);
        list.scrollTop = list.scrollHeight;
    }
    
    renderAgentCards() {
        const container = document.getElementById('agents-container');
        if (!container) return; // 元素不存在时直接返回（如在登录页面）
        container.innerHTML = '';

        // 只显示当前聊天室中的agent
        const agentsToShow = this.currentRoomAgents.length > 0 ? this.currentRoomAgents : this.agents;
        const count = agentsToShow.length;

        for (let i = 0; i < count; i++) {
            const agent = agentsToShow[i];
            const card = document.createElement('div');
            card.id = `agent-${i}-card`;

            // 判断是对象还是字符串
            const agentId = typeof agent === 'string' ? agent : agent.id;
            const agentPorts = typeof agent === 'object' ? `x-client: ${agent.xclientPort}, agentcore: ${agent.agentcorePort}` : '';

            // 获取在线状态
            const status = this.memberStatus[this.currentChannelId]?.[agentId];
            const isOnline = status ? status.online : null;
            const statusHtml = isOnline === null ?
                '<div class="status unknown" id="agent-${i}-status">未知</div>' :
                (isOnline ?
                    '<div class="status-dot-card online"></div><div class="status-text online" id="agent-${i}-status">在线</div>' :
                    '<div class="status-dot-card offline"></div><div class="status-text offline" id="agent-${i}-status">离线</div>');

            // 生成可读的显示名称
            let displayName = agentId;
            if (agentId.startsWith('user_')) {
                // 如果是当前用户，显示昵称
                if (this.currentUser && agentId === `user_${this.currentUser.id}`) {
                    displayName = this.currentUser.nickname || this.currentUser.username;
                } else {
                    displayName = agentId; // 其他用户只显示 ID
                }
            }

            card.innerHTML = `
                <div class="agent-name">${displayName}</div>
                <div class="agent-ports">${agentPorts}</div>
                <div class="agent-status-row">
                    ${statusHtml}
                </div>
            `;
            container.appendChild(card);
        }
    }

    updateAgentCards() {
        // 更新当前聊天室中所有 Agent 的在线状态
        const agentsToShow = this.currentRoomAgents.length > 0 ? this.currentRoomAgents : this.agents;

        agentsToShow.forEach((agent, index) => {
            const card = document.getElementById(`agent-${index}-card`);
            const agentId = typeof agent === 'string' ? agent : agent.id;

            if (card) {
                // 获取在线状态
                const status = this.memberStatus[this.currentChannelId]?.[agentId];
                const isOnline = status ? status.online : null;
                const statusHtml = isOnline === null ?
                    '<div class="status unknown" id="agent-${index}-status">未知</div>' :
                    (isOnline ?
                        '<div class="status-dot-card online"></div><div class="status-text online" id="agent-${index}-status">在线</div>' :
                        '<div class="status-dot-card offline"></div><div class="status-text offline" id="agent-${index}-status">离线</div>');

                card.innerHTML = `
                    <div class="agent-name">${agentId}</div>
                    <div class="agent-ports">${typeof agent === 'object' ? `x-client: ${agent.xclientPort}, agentcore: ${agent.agentcorePort}` : ''}</div>
                    <div class="agent-status-row">
                        ${statusHtml}
                    </div>
                `;
            }
        });
    }
    
    updateCoordinatorButtons() {
        // 主页面按钮
        const startBtn = document.getElementById('start-coordinator');
        const stopBtn = document.getElementById('stop-coordinator');
        if (startBtn) startBtn.disabled = this.isCoordinatorRunning;
        if (stopBtn) stopBtn.disabled = !this.isCoordinatorRunning;

        // 抽屉内按钮
        const drawerStartBtn = document.getElementById('drawer-start-coordinator');
        const drawerStopBtn = document.getElementById('drawer-stop-coordinator');
        if (drawerStartBtn) drawerStartBtn.disabled = this.isCoordinatorRunning;
        if (drawerStopBtn) drawerStopBtn.disabled = !this.isCoordinatorRunning;
    }

    updateAgentsButtons() {
        // 主页面按钮
        const startBtn = document.getElementById('start-agents');
        const stopBtn = document.getElementById('stop-agents');
        if (startBtn) startBtn.disabled = !this.isCoordinatorRunning || this.isAgentsRunning;
        if (stopBtn) stopBtn.disabled = !this.isAgentsRunning;

        // 抽屉内按钮
        const drawerStartBtn = document.getElementById('drawer-start-agents');
        const drawerStopBtn = document.getElementById('drawer-stop-agents');
        if (drawerStartBtn) drawerStartBtn.disabled = !this.isCoordinatorRunning || this.isAgentsRunning;
        if (drawerStopBtn) drawerStopBtn.disabled = !this.isAgentsRunning;
    }

    enableStartAgents() {
        const startBtn = document.getElementById('start-agents');
        const drawerStartBtn = document.getElementById('drawer-start-agents');
        if (startBtn) startBtn.disabled = this.isAgentsRunning;
        if (drawerStartBtn) drawerStartBtn.disabled = this.isAgentsRunning;
    }

    disableStartAgents() {
        const startBtn = document.getElementById('start-agents');
        const drawerStartBtn = document.getElementById('drawer-start-agents');
        if (startBtn) startBtn.disabled = true;
        if (drawerStartBtn) drawerStartBtn.disabled = true;
    }

    updateStatus(elementId, className, text) {
        const element = document.getElementById(elementId);
        if (element) {
            element.className = `status ${className}`;
            element.textContent = text;
        }
    }
    
    onSenderModeChange(e) {
        const mode = e.target.value;
        const agentSender = document.getElementById('agent-sender');
        
        if (mode === 'agent') {
            agentSender.style.display = 'inline-block';
            if (this.agents.length > 0 && !agentSender.value) {
                agentSender.value = this.agents[0].id;
            }
        } else {
            agentSender.style.display = 'none';
        }
    }
    
    updateAgentSenderSelect() {
        const select = document.getElementById('agent-sender');
        select.innerHTML = '';
        
        // 只显示当前聊天室中在线的agent
        const members = this.roomMembers[this.currentChannelId] || [];
        const onlineAgents = members.filter(member => 
            member.memberType === 'agent' && 
            this.memberStatus[this.currentChannelId]?.[member.agentId]?.online
        );
        
        onlineAgents.forEach(member => {
            const option = document.createElement('option');
            option.value = member.agentId;
            option.textContent = member.agentId;
            select.appendChild(option);
        });
        
        // 如果当前是agent模式且有在线agent，设置默认值
        const senderMode = document.getElementById('sender-mode').value;
        if (senderMode === 'agent' && onlineAgents.length > 0) {
            select.value = onlineAgents[0].agentId;
            select.style.display = 'inline-block';
        } else if (onlineAgents.length === 0) {
            select.style.display = 'none';
        }
    }
    
    updateRoomAgentsSelect() {
        const container = document.getElementById('room-agents-checkboxes');
        container.innerHTML = '';
        
        this.agents.forEach(agent => {
            const label = document.createElement('label');
            label.className = 'agent-checkbox';
            label.innerHTML = `
                <input type="checkbox" value="${agent.id}">
                <span>${agent.id}</span>
            `;
            label.querySelector('input').addEventListener('change', () => this.updateCreateRoomButton());
            container.appendChild(label);
        });
        
        this.updateCreateRoomButton();
    }
    
    updateCreateRoomButton() {
        const name = document.getElementById('room-name-input')?.value.trim() || '';
        const checkboxes = document.querySelectorAll('#room-agents-checkboxes input:checked');
        const selectedAgents = Array.from(checkboxes).map(cb => cb.value);
        const btn = document.getElementById('create-room-btn');

        if (btn) {
            btn.disabled = !name || selectedAgents.length === 0 || !this.isCoordinatorRunning;
        }
    }
    
    async createRoom() {
        const name = document.getElementById('room-name-input').value.trim();
        const checkboxes = document.querySelectorAll('#room-agents-checkboxes input:checked');
        const selectedAgents = Array.from(checkboxes).map(cb => cb.value);
        
        this.log('info', `正在创建聊天室: ${name}`);
        
        try {
            const res = await fetch('/api/room/create', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: name,
                    agents: selectedAgents
                })
            });
            const data = await res.json();
            if (data.success) {
                this.log('success', `聊天室 ${name} 创建成功`);
                document.getElementById('room-name-input').value = '';
                // 取消所有checkbox的选择
                document.querySelectorAll('#room-agents-checkboxes input').forEach(cb => cb.checked = false);
                this.updateCreateRoomButton();
                this.loadRooms();

                // 自动切换到新创建的聊天室
                const roomId = data.room_id;
                const room = this.rooms.find(r => r.id === roomId);
                if (room) {
                    setTimeout(() => {
                        this.currentChannelId = room.id;
                        this.currentRoomAgents = room.agents || [];
                        this.currentMembers = null;
                        document.getElementById('chat-area').style.display = 'flex';
                        document.getElementById('room-actions').style.display = 'flex';
                        this.clearMessages();
                        this.renderRooms();
                        this.renderMembersPanel();
                        this.updateAgentSenderSelect();
                        this.log('info', `已切换到聊天室: ${room.name}`);
                    }, 500);
                }
            } else {
                this.log('error', '创建聊天室失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '创建聊天室时出错: ' + err.message);
        }
    }

    // 加入聊天室，返回 true/false 表示是否成功
    async joinRoom() {
        if (!this.currentChannelId) {
            this.log('warn', '请先选择一个聊天室');
            return false;
        }
        if (!this.currentUser) {
            this.log('warn', '请先登录');
            return false;
        }

        try {
            const res = await fetch('/api/room/join', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    room_id: this.currentChannelId,
                    member_id: this.currentUser.id,
                    member_type: 'user'
                })
            });
            const data = await res.json();
            if (data.success) {
                // 保存 session_id，用于 WS 连接验证
                console.log('[joinRoom] join 成功，session_id:', data.session_id, 'type:', typeof data.session_id);
                this.currentSessionId = data.session_id;
                console.log('[joinRoom] currentSessionId 已保存:', this.currentSessionId);
                this.log('success', `已加入聊天室: ${this.getCurrentRoomName()}`);

                // join 成功，启用发送按钮
                const sendBtn = document.getElementById('send-btn');
                if (sendBtn) sendBtn.disabled = false;

                // 如果 WebSocket 未连接，先连接（携带 session_id）
                if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
                    console.log('[joinRoom] 准备建立 WS 连接，currentSessionId:', this.currentSessionId);
                    await this.connectWebSocketWithSession(this.currentChannelId, this.currentSessionId);
                }

                // 等待一小段时间后获取成员状态
                await new Promise(resolve => setTimeout(resolve, 500));
                await this.checkMemberStatus();

                return true;
            } else {
                this.log('error', '加入聊天室失败: ' + (data.error || data.message));
                // join 失败，禁用发送按钮
                const sendBtn = document.getElementById('send-btn');
                if (sendBtn) sendBtn.disabled = true;
                // 清除 session_id
                this.currentSessionId = null;
                return false;
            }
        } catch (err) {
            this.log('error', '加入聊天室时出错: ' + err.message);
            // 出错也要禁用发送按钮
            const sendBtn = document.getElementById('send-btn');
            if (sendBtn) sendBtn.disabled = true;
            this.currentSessionId = null;
            return false;
        }
    }

    // 使用 session_id 建立 WebSocket 连接
    async connectWebSocketWithSession(roomId, sessionId) {
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }
        if (!this.currentUser) {
            this.log('warn', '请先登录');
            return;
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = protocol + '//' + window.location.host + '/ws/chat?user_id=' + encodeURIComponent(this.currentUser.id) + '&room_id=' + encodeURIComponent(roomId) + '&session_id=' + encodeURIComponent(sessionId);
        console.log('[WS] Connecting with session:', wsUrl);
        this.ws = new WebSocket(wsUrl);

        this.ws.onopen = () => {
            this.log('success', 'WebSocket 连接成功');
            // 用户加入聊天室
            this.sendJoin();
            // 注意：不在这里调用 checkMemberStatus，由 joinRoom 统一调用
        };
        
        this.ws.onmessage = (event) => {
            const data = JSON.parse(event.data);
            this.handleMessage(data);
        };
        
        this.ws.onerror = (event) => {
            let errorMsg = '未知错误';
            if (event && event.type === 'error') {
                errorMsg = '连接错误';
            }
            if (event && event.target && event.target.readyState === 3) {
                errorMsg = '连接已关闭';
            }
            this.log('error', 'WebSocket 错误: ' + errorMsg);
        };
        
        this.ws.onclose = () => {
            this.log('info', 'WebSocket 连接关闭');
            this.ws = null;
            // 不立即调用 leave 接口，因为可能是连接中断或切换聊天室导致的
            // 用户离线状态由后端通过心跳超时检测
        };
    }

    // 退出聊天室
    async leaveRoom() {
        if (!this.currentChannelId) {
            this.log('warn', '请先选择一个聊天室');
            return;
        }
        if (!this.currentUser) {
            this.log('warn', '请先登录');
            return;
        }

        try {
            const res = await fetch('/api/room/leave', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    room_id: this.currentChannelId,
                    member_id: this.currentUser.id
                })
            });
            const data = await res.json();
            if (data.success) {
                this.log('success', `已退出聊天室: ${this.getCurrentRoomName()}`);
                // 断开 WebSocket 连接
                this.disconnectWebSocket();
                // 清空聊天室状态
                this.currentChannelId = '';
                this.currentRoomAgents = [];
                this.currentMembers = null;
                document.getElementById('chat-area').style.display = 'none';
                document.getElementById('room-actions').style.display = 'none';
                this.clearMessages();
                // 刷新聊天室列表
                await this.loadRooms();
            } else {
                this.log('error', '退出聊天室失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '退出聊天室时出错: ' + err.message);
        }
    }

    // 获取当前聊天室名称
    getCurrentRoomName() {
        const room = this.rooms.find(r => r.id === this.currentChannelId);
        return room ? room.name : this.currentChannelId;
    }

    // 加载聊天室列表
    async loadRooms() {
        try {
            const res = await fetch('/api/rooms');
            const data = await res.json();
            if (data.success && data.rooms) {
                // 将后端返回的 room_id 转换为 id，适配前端
                const normalizedRooms = data.rooms.map(room => ({
                    id: room.room_id,  // 后端返回 room_id，前端使用 id
                    room_id: room.room_id,
                    name: room.name,
                    created: room.created_at,
                    description: room.description,
                    created_by: room.created_by
                }));

                // 简单处理，只保存基本信息的聊天室列表
                // 不再为每个聊天室调用 members 接口（用户加入时再获取成员状态）
                const simplifiedRooms = normalizedRooms.map(room => ({
                    id: room.room_id,
                    room_id: room.room_id,
                    name: room.name,
                    created: room.created_at,
                    description: room.description,
                    created_by: room.created_by
                }));

                this.rooms = simplifiedRooms;

                this.renderRooms();

                // 只对当前聊天室获取一次成员状态（加入时再获取完整状态）
                // 不再对所有聊天室都查询 members 接口
                // if (this.currentChannelId) {
                //     await this.checkMemberStatus(this.currentChannelId);
                // }

                // 如果当前有聊天室，更新currentRoomAgents
                if (this.currentChannelId) {
                    const currentRoom = this.rooms.find(r => r.id === this.currentChannelId);
                    if (currentRoom) {
                        this.currentRoomAgents = currentRoom.agents || [];
                        this.renderAgentCards();
                        this.updateAgentSenderSelect();
                    } else {
                        // 当前聊天室不存在了，清空
                        this.currentChannelId = '';
                        this.currentRoomAgents = [];
                        document.getElementById('agents-panel').style.display = 'none';
                        document.getElementById('chat-panel').style.display = 'none';
                    }
                } else if (this.rooms.length > 0) {
                    // 如果没有选中聊天室，自动选中第一个
                    console.log('[loadRooms] No currentChannelId, selecting first room:', this.rooms[0]);
                    await this.selectRoom(this.rooms[0]);
                }
            } else {
                console.log('[loadRooms] No rooms or success=false, rooms:', data.rooms);
                this.rooms = [];
                this.renderRooms();
            }
        } catch (err) {
            console.error('[loadRooms] Error:', err);
            this.log('error', '加载聊天室列表失败: ' + err.message);
        }
    }

    renderRooms() {
        const container = document.getElementById('rooms-list');
        container.innerHTML = '';

        if (this.rooms.length === 0) {
            container.innerHTML = '<p style="color: #999;">暂无聊天室，点击上方按钮创建</p>';
            return;
        }

        this.rooms.forEach(room => {
            const roomItem = document.createElement('div');
            roomItem.className = `room-item ${room.id === this.currentChannelId ? 'active' : ''}`;

            // 从缓存中获取成员列表来展示（包含 Agent 和 User）
            const cachedMembers = this.roomMembers[room.id] || [];
            let membersHtml = '';

            if (cachedMembers.length > 0) {
                // 使用缓存的成员列表
                membersHtml = cachedMembers.map(member => {
                    const status = this.memberStatus[room.id]?.[member.agentId];
                    const isOnline = status ? status.online : false;
                    const statusDot = isOnline ? '<span class="status-dot online"></span>' : '<span class="status-dot offline"></span>';

                    // 生成可读的显示名称
                    let displayName = member.agentId;
                    if (member.memberType === 'user') {
                        if (this.currentUser && member.agentId === `user_${this.currentUser.id}`) {
                            displayName = this.currentUser.nickname || this.currentUser.username;
                        }
                    }

                    return `<span class="agent-tag">${statusDot}${displayName}</span>`;
                }).join('');
            } else {
                // 如果没有缓存，使用创建时选择的 agents
                membersHtml = (room.agents || []).map(agent => {
                    const agentId = typeof agent === 'string' ? agent : agent.id;
                    const status = this.memberStatus[room.id]?.[agentId];
                    const isOnline = status ? status.online : null;
                    const statusDot = isOnline === null ? '' : (isOnline ? '<span class="status-dot online"></span>' : '<span class="status-dot offline"></span>');
                    return `<span class="agent-tag">${statusDot}${agentId}</span>`;
                }).join('');
            }

            roomItem.innerHTML = `
                <div class="room-info">
                    <span class="room-name">${room.name}</span>
                </div>
                <button class="delete-room-btn" onclick="event.stopPropagation(); app.deleteRoom('${room.id}')">删除</button>
            `;
            roomItem.addEventListener('click', () => this.selectRoom(room));
            container.appendChild(roomItem);
        });
    }

    async deleteRoom(roomId) {
        const room = this.rooms.find(r => r.id === roomId);
        if (!room) return;
        
        if (!confirm(`确定要删除聊天室 "${room.name}" 吗？`)) {
            return;
        }
        
        this.log('info', `正在删除聊天室: ${room.name}`);
        
        try {
            const res = await fetch('/api/room/delete', {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ room_id: roomId })
            });
            const data = await res.json();
            if (data.success) {
                this.log('success', `聊天室 ${room.name} 删除成功`);

                if (this.currentChannelId === roomId) {
                    this.currentChannelId = '';
                    this.currentRoomAgents = [];
                    this.currentMembers = null;
                    document.getElementById('chat-area').style.display = 'none';
                    document.getElementById('room-actions').style.display = 'none';
                    this.clearMessages();
                }

                this.loadRooms();
            } else {
                this.log('error', '删除聊天室失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '删除聊天室时出错: ' + err.message);
        }
    }
    
    async selectRoom(room) {
        if (room.id === this.currentChannelId) return;

        // 如果有连接，先断开 WebSocket 连接
        if (this.ws) {
            this.log('info', '正在断开与当前聊天室的 WebSocket 连接...');
            this.disconnectWebSocket();
            // 等待连接完全关闭
            await new Promise(resolve => setTimeout(resolve, 100));
        }

        this.currentChannelId = room.id;
        this.currentRoomAgents = room.agents || [];
        this.currentMembers = null; // 清空成员列表
        this.clearMessages();
        this.renderRooms();

        // 显示 chat-area 和 room-actions
        document.getElementById('chat-area').style.display = 'flex';
        document.getElementById('room-actions').style.display = 'flex';

        // 初始化成员面板
        this.renderMembersPanel();

        // 更新agent-sender选择器，只显示当前聊天室中的agent
        this.updateAgentSenderSelect();

        // 检查用户是否已登录
        if (!this.currentUser) {
            this.log('warn', '请先登录才能加入聊天室');
            const sendBtn3 = document.getElementById('send-btn');
            if (sendBtn3) sendBtn3.disabled = true;
            return;
        }

        // 用户已登录，自动加入聊天室
        const joinResult = await this.joinRoom();

        // 如果 join 失败，不继续加载历史和成员状态
        if (!joinResult) {
            this.log('warn', '无法加入聊天室，无法加载历史记录');
            // 禁用发送按钮
            const sendBtn4 = document.getElementById('send-btn');
            if (sendBtn4) sendBtn4.disabled = true;
            return;
        }

        // 通过 REST API 获取历史消息
        await this.loadHistory();

        // 注意：成员状态检查已在 joinRoom 中调用，这里不再重复调用

        this.log('info', `已切换到聊天室: ${room.name}`);
    }

    // 通过 REST API 获取历史消息
    async loadHistory(count = 100) {
        if (!this.currentChannelId) return;

        try {
            const res = await fetch(`/api/room/history?room_id=${encodeURIComponent(this.currentChannelId)}&count=${count}`);
            const data = await res.json();

            if (data.success && data.messages) {
                this.clearMessages();
                data.messages.forEach(msg => {
                    // 使用消息中的时间戳（如果存在）
                    const timestamp = msg.created_at ? new Date(msg.created_at * 1000) : null;
                    this.addMessage(msg.sender, msg.content, timestamp);
                });
            }
        } catch (err) {
            console.error('获取历史消息失败:', err);
        }
    }

    clearMessages() {
        const list = document.getElementById('message-list');
        list.innerHTML = '';
    }
    
    async startCoordinator() {
        this.log('info', '正在启动协调器...');
        try {
            const res = await fetch('/api/coordinator/start', { method: 'POST' });
            const data = await res.json();
            if (data.success) {
                this.isCoordinatorRunning = true;
                this.updateCoordinatorButtons();
                this.updateStatus('coordinator-status', 'running', '运行中');
                this.log('success', '协调器启动成功');
                this.enableStartAgents();

                // 只有在用户已登录时才连接 WebSocket 和加载聊天室
                if (this.currentUser) {
                    setTimeout(() => {
                        this.connectWebSocket();
                    }, 500);
                    setTimeout(() => {
                        this.loadRooms();
                    }, 1000);
                }
            } else {
                this.log('error', '协调器启动失败: ' + data.error);
            }
        } catch (err) {
            this.log('error', '启动协调器时出错: ' + err.message);
        }
    }
}

document.addEventListener('DOMContentLoaded', () => {
    new AgentTest();
});