/*
  Arquitetura:
  - Estado central em memoria: workflow, viewport, selecao, painel UI e execucao
  - Renderizacao desacoplada para paleta, blocos, conexoes, inspetor e console
  - Persistencia em localStorage do workflow, JSONs de execucao e estado visual
  - Geracao local de blueprint e esqueleto Go para aplicacoes com go-code-blocks
  - O compilador visual monta um app plan simples para orientar implementacao e export
*/

(function () {
  const STORAGE_KEY = "go-code-blocks-studio-v1";
  const STAGE_WIDTH = 2600;
  const STAGE_HEIGHT = 1800;
  const DEFAULT_NODE_SIZE = { width: 250, height: 170 };

  const awsServiceIcons = {
    dynamodb: awsIcon("dynamodb.svg"),
    rds: awsIcon("rds.svg"),
    redis: awsIcon("elasticache-for-redis.svg"),
    s3: awsIcon("s3.svg"),
    parameterstore: awsIcon("parameter-store.svg"),
    secretsmanager: awsIcon("secrets-manager.svg"),
    restapi: awsIcon("api-gateway.svg")
  };

  const nodeCatalog = {
    server: {
      label: "server",
      role: "entry",
      accentClass: "type-request",
      icon: "S",
      description: "HTTP, Lambda ou TCP como entrada da aplicacao.",
      defaults: () => ({
        title: "orders-api",
        description: "Entrada principal do servico.",
        metadata: {
          requestName: "httpServer",
          scenarioName: "Orders API",
          sigla: "ORD",
          siglaApp: "ORDERS-API",
          serviceName: "orders-api",
          serverKind: "HTTP",
          routeMethod: "POST",
          routePath: "/orders/:id",
          port: "8080",
          middleware: "Logging, Recovery",
          payloadText: formatJsonForEditor(defaultExecutionPayload())
        }
      })
    },
    flow: {
      label: "flow",
      role: "flow",
      accentClass: "type-aggregator",
      icon: "F",
      description: "Pipeline declarativo que encadeia steps.",
      defaults: () => ({
        title: "createOrder",
        description: "Flow principal da rota.",
        metadata: {
          stepId: "createOrder",
          handlerName: "createOrder",
          operation: "flow.New",
          stateKey: "order"
        }
      })
    },
    decision: {
      label: "decision",
      role: "decision",
      accentClass: "type-conditional",
      icon: "D",
      description: "Regras CEL para validate e decide.",
      defaults: () => ({
        title: "validateCustomer",
        description: "Valida ou ramifica a execucao conforme uma regra CEL.",
        metadata: {
          strategy: "first-match",
          celExpression: "customer_type == 'PJ'",
          ruleName: "is-pj",
          successAction: "next_step",
          successTarget: "",
          successHttpStatus: "200",
          successMessage: "Condicao atendida",
          failureAction: "return_error",
          failureTarget: "",
          failureHttpStatus: "403",
          failureMessage: "Cliente nao autorizado"
        },
        conditions: [
          { id: "success", label: "Sucesso", expression: "customer_type == 'PJ'" },
          { id: "failure", label: "Falha", expression: "fallback" }
        ]
      })
    },
    transform: {
      label: "transform",
      role: "transform",
      accentClass: "type-aggregator",
      icon: "T",
      description: "Builder para compor payloads a partir do state.",
      defaults: () => ({
        title: "buildOrder",
        description: "Monta dados derivados antes da resposta.",
        metadata: {
          stepId: "buildOrder",
          operation: "transform.New",
          stateKey: "order",
          expression: "transform.New(s, req).Set(\"status\", \"created\").Build()"
        }
      })
    },
    output: {
      label: "output",
      role: "output",
      accentClass: "type-aggregator",
      icon: "O",
      description: "Resposta HTTP ou payload REST declarativo.",
      defaults: () => ({
        title: "respondCreated",
        description: "Monta a resposta final do handler.",
        metadata: {
          aggregationName: "respond",
          operation: "output.JSON",
          expression: "output.Created(\"order\")",
          responseStatus: "201",
          stateKey: "order"
        }
      })
    },
    dynamodb: createIntegrationCatalog("dynamodb", awsServiceIcons.dynamodb, "DynamoDB tipado com PutItem, GetItem, Query e Scan.", "customersDB", "GetItem", "customers-prod"),
    rds: createIntegrationCatalog("rds", awsServiceIcons.rds, "RDS/Aurora com QueryOne, QueryAll, Exec e Tx.", "ordersDB", "QueryOne", "postgres://user:pass@localhost:5432/app"),
    redis: createIntegrationCatalog("redis", awsServiceIcons.redis, "Cache Redis com strings, JSON e hashes.", "cache", "GetJSON", "localhost:6379"),
    s3: createIntegrationCatalog("s3", awsServiceIcons.s3, "Storage S3 com put, get e delete de objetos.", "bucket", "GetObject", "my-bucket"),
    parameterstore: createIntegrationCatalog("parameterstore", awsServiceIcons.parameterstore, "SSM Parameter Store para parametros por path.", "params", "GetParameter", "/myapp/dev"),
    secretsmanager: createIntegrationCatalog("secretsmanager", awsServiceIcons.secretsmanager, "Secrets Manager para segredos simples e JSON.", "secrets", "GetSecretJSON", "myapp/db"),
    restapi: createIntegrationCatalog("restapi", awsServiceIcons.restapi, "Cliente REST com auth, retry, FanOut e Pipeline.", "billingAPI", "CallJSON", "https://api.example.com"),
    cnab: createIntegrationCatalog("cnab", "N", "Parser declarativo de arquivos CNAB 240 e 400.", "remessaCNAB", "Parse", "CNAB240")
  };

  const state = {
    nodes: [],
    edges: [],
    viewport: { x: 200, y: 120, scale: 1 },
    selectedNodeId: null,
    pendingConnection: null,
    drag: null,
    pan: null,
    contextNodeId: null,
    activeExecutionNodeIds: [],
    activeExecutionNodeStates: {},
    ui: {
      leftCollapsed: false,
      rightCollapsed: false,
      bottomCollapsed: false,
      activeBottomTab: "code"
    },
    execution: {
      serviceName: "orders-api",
      mocksText: "",
      payloadText: "",
      manifestPreview: "",
      logs: [],
      stepNodeMap: {},
      terminalNodeMap: {},
      running: false
    }
  };

  const refs = {
    palette: document.getElementById("nodePalette"),
    nodeLayer: document.getElementById("nodeLayer"),
    edgeLayer: document.getElementById("edgeLayer"),
    inspectorContent: document.getElementById("inspectorContent"),
    workspaceViewport: document.getElementById("workspaceViewport"),
    workspaceSurface: document.getElementById("workspaceSurface"),
    workspaceStage: document.getElementById("workspaceStage"),
    storageStatus: document.getElementById("storageStatus"),
    nodeCount: document.getElementById("nodeCount"),
    edgeCount: document.getElementById("edgeCount"),
    zoomLabel: document.getElementById("zoomLabel"),
    importInput: document.getElementById("importInput"),
    contextMenu: document.getElementById("contextMenu"),
    terminal: document.getElementById("terminal"),
    appCode: document.getElementById("appCode"),
    serviceNameInput: document.getElementById("serviceNameInput"),
    executionStatus: document.getElementById("executionStatus"),
    bottomTabs: Array.from(document.querySelectorAll("[data-bottom-tab]")),
    bottomPanels: Array.from(document.querySelectorAll("[data-bottom-panel]"))
  };

  function createIntegrationCatalog(packageName, icon, description, blockName, operation, endpoint) {
    return {
      label: packageName,
      role: "integration",
      accentClass: "type-fetch",
      icon,
      description,
      defaults: () => ({
        title: blockName,
        description,
        metadata: {
          stepId: blockName,
          endpoint,
          method: "POST",
          resourceName: blockName,
          source: blockName,
          resourceType: packageName,
          operation,
          lookupKey: "req.PathParam(\"id\")",
          mockText: "",
          successAction: "next_step",
          successTarget: "",
          successHttpStatus: "200",
          successMessage: "Operacao concluida",
          failureAction: "return_error",
          failureTarget: "",
          failureHttpStatus: "500",
          failureMessage: "Falha no bloco"
        }
      })
    };
  }

  function awsIcon(fileName) {
    return `<img class="aws-icon-img" src="icons/aws/${fileName}" alt="" aria-hidden="true" draggable="false">`;
  }

  function getIconClass(definition) {
    return definition.icon.includes("aws-icon-img") ? " aws-service-icon" : "";
  }

  initialize();

  function initialize() {
    bindStaticEvents();
    renderPalette();

    const restored = loadFromStorage();
    if (restored) {
      applyStoredState(restored);
    } else {
      loadExampleWorkflow();
      initializeExecutionDefaults();
      logMessage("Interface pronta. Fluxo de exemplo carregado.", "sys");
    }

    tryRefreshManifestPreview();
    renderUIState();
    renderApp();
    fitView();
  }

  function bindStaticEvents() {
    const shouldSkipBottomPanelToggle = (event) => event.target.closest("#runWorkflowBottomBtn, #clearTerminalBtn, [data-bottom-tab]");

    bindIfPresent("fitViewBtn", "click", fitView);
    bindIfPresent("resetViewBtn", "click", resetView);
    document.getElementById("zoomInBtn").addEventListener("click", () => setZoom(state.viewport.scale * 1.12));
    document.getElementById("zoomOutBtn").addEventListener("click", () => setZoom(state.viewport.scale / 1.12));
    document.getElementById("centerSelectionBtn").addEventListener("click", centerSelection);
    bindIfPresent("exportBtn", "click", exportWorkflow);
    bindIfPresent("importBtn", "click", () => refs.importInput.click());
    bindIfPresent("resetWorkflowBtn", "click", resetToExample);
    document.getElementById("toggleLeftSidebarBtn").addEventListener("click", () => togglePanel("leftCollapsed"));
    document.getElementById("toggleRightSidebarBtn").addEventListener("click", () => togglePanel("rightCollapsed"));
    document.getElementById("toggleBottomPanelBtn").addEventListener("click", (event) => {
      if (shouldSkipBottomPanelToggle(event)) {
        return;
      }
      togglePanel("bottomCollapsed");
    });
    document.getElementById("toggleBottomPanelBtn").addEventListener("keydown", (event) => {
      if (event.key !== "Enter" && event.key !== " ") {
        return;
      }
      if (shouldSkipBottomPanelToggle(event)) {
        return;
      }
      event.preventDefault();
      togglePanel("bottomCollapsed");
    });
    document.getElementById("runWorkflowBottomBtn").addEventListener("click", runWorkflow);
    document.getElementById("clearTerminalBtn").addEventListener("click", clearTerminal);
    refs.bottomTabs.forEach((tab) => tab.addEventListener("click", () => setBottomTab(tab.dataset.bottomTab)));
    refs.importInput.addEventListener("change", importWorkflow);

    refs.serviceNameInput.addEventListener("input", () => {
      state.execution.serviceName = refs.serviceNameInput.value.trim();
      tryRefreshManifestPreview();
    });

    refs.workspaceSurface.addEventListener("wheel", handleWheel, { passive: false });
    refs.workspaceSurface.addEventListener("pointerdown", handleSurfacePointerDown);
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    window.addEventListener("resize", renderEdges);

    document.addEventListener("keydown", handleKeydown);
    document.addEventListener("click", () => hideContextMenu());
    refs.contextMenu.addEventListener("click", handleContextMenuAction);
  }

  function bindIfPresent(id, eventName, handler) {
    const element = document.getElementById(id);
    if (element) {
      element.addEventListener(eventName, handler);
    }
  }

  function renderPalette() {
    refs.palette.innerHTML = "";
    Object.entries(nodeCatalog).forEach(([type, definition]) => {
      const button = document.createElement("button");
      button.className = `tool-btn tool-${type}`;
      button.type = "button";
      button.title = definition.label;
      button.setAttribute("aria-label", `Adicionar ${definition.label}`);
      const iconClass = getIconClass(definition);
      button.innerHTML = `
        <span class="tool-icon ${definition.accentClass}${iconClass}">${definition.icon}</span>
        <span class="tool-copy">
          <strong>${definition.label}</strong>
          <span>${definition.description}</span>
        </span>
      `;
      button.addEventListener("click", () => addNodeToVisibleArea(type));
      refs.palette.appendChild(button);
    });
  }

  function loadExampleWorkflow() {
    const request = createNode("server", {
      id: "node-request",
      x: 120,
      y: 120,
      title: "orders-api",
      metadata: {
        requestName: "httpServer",
        scenarioName: "Orders API",
        sigla: "ORD",
        siglaApp: "ORDERS-API",
        serviceName: "orders-api",
        serverKind: "HTTP",
        routeMethod: "POST",
        routePath: "/orders/:id",
        port: "8080"
      }
    });

    const loadCustomer = createNode("dynamodb", {
      id: "node-load-customer",
      x: 700,
      y: 90,
      title: "loadCustomer",
      description: "Busca o cliente no DynamoDB.",
      metadata: {
        endpoint: "customers-prod",
        method: "GET",
        resourceName: "customersDB",
        source: "customersDB",
        resourceType: "dynamodb",
        operation: "GetItem",
        lookupKey: "req.PathParam(\"id\")",
        successAction: "next_step",
        successTarget: "",
        successHttpStatus: "200",
        successMessage: "Cliente carregado",
        failureAction: "return_error",
        failureTarget: "",
        failureHttpStatus: "404",
        failureMessage: "Cliente nao encontrado"
      }
    });

    const validateCustomer = createNode("decision", {
      id: "node-validate-customer",
      x: 500,
      y: 320,
      title: "validateCustomer",
      description: "Aplica uma regra CEL antes de criar o pedido.",
      conditions: [
        { id: "success", label: "Sucesso", expression: "customer_type == 'PJ'" },
        { id: "failure", label: "Falha", expression: "fallback" }
      ],
      metadata: {
        celExpression: "customer_type == 'PJ'",
        ruleName: "is-pj",
        successAction: "next_step",
        successTarget: "",
        successHttpStatus: "200",
        successMessage: "Condicao atendida",
        failureAction: "return_error",
        failureTarget: "",
        failureHttpStatus: "403",
        failureMessage: "Cliente nao autorizado"
      }
    });

    const saveOrder = createNode("dynamodb", {
      id: "node-save-order",
      x: 1120,
      y: 290,
      title: "saveOrder",
      description: "Persiste o pedido em outro bloco DynamoDB.",
      metadata: {
        endpoint: "orders-prod",
        method: "POST",
        resourceName: "ordersDB",
        source: "ordersDB",
        resourceType: "dynamodb",
        operation: "PutItem",
        lookupKey: "state[\"order\"]",
        successAction: "next_step",
        successTarget: "",
        successHttpStatus: "200",
        successMessage: "Pedido salvo",
        failureAction: "return_error",
        failureTarget: "",
        failureHttpStatus: "500",
        failureMessage: "Falha ao salvar pedido"
      }
    });

    const response = createNode("output", {
      id: "node-response",
      x: 1760,
      y: 320,
      title: "respondCreated",
      metadata: {
        aggregationName: "respond",
        expression: "output.Created(\"order\")",
        responseStatus: "201",
        stateKey: "order"
      }
    });

    state.nodes = [
      request,
      loadCustomer,
      validateCustomer,
      saveOrder,
      response
    ];

    state.edges = [
      createEdge(request.id, "out-main", loadCustomer.id, "in-main"),
      createEdge(loadCustomer.id, "success", validateCustomer.id, "in-main"),
      createEdge(validateCustomer.id, "success", saveOrder.id, "in-main"),
      createEdge(saveOrder.id, "success", response.id, "in-main")
    ];

    state.selectedNodeId = validateCustomer.id;
    state.pendingConnection = null;
  }

  function initializeExecutionDefaults() {
    state.execution.serviceName = "orders-api";
    state.execution.mocksText = formatJsonForEditor(defaultExecutionMocks());
    state.execution.payloadText = formatJsonForEditor(defaultExecutionPayload());
  }

  function createNode(type, overrides = {}) {
    const definition = nodeCatalog[type];
    const defaults = definition.defaults();
    const node = {
      id: overrides.id || createId("node"),
      type,
      x: overrides.x ?? 240,
      y: overrides.y ?? 200,
      width: overrides.width || DEFAULT_NODE_SIZE.width,
      height: overrides.height || DEFAULT_NODE_SIZE.height,
      title: overrides.title || defaults.title,
      description: overrides.description ?? defaults.description,
      metadata: { ...defaults.metadata, ...(overrides.metadata || {}) },
      conditions: overrides.conditions ? clone(overrides.conditions) : clone(defaults.conditions || [])
    };
    if (isDecisionNode(node)) {
      ensureConditionalPorts(node);
    }
    return node;
  }

  function getNodeRole(nodeOrType) {
    const type = typeof nodeOrType === "string" ? nodeOrType : nodeOrType?.type;
    return nodeCatalog[type]?.role || "integration";
  }

  function isEntryNode(nodeOrType) {
    return getNodeRole(nodeOrType) === "entry";
  }

  function isDecisionNode(nodeOrType) {
    return getNodeRole(nodeOrType) === "decision";
  }

  function isIntegrationNode(nodeOrType) {
    return getNodeRole(nodeOrType) === "integration";
  }

  function isOutputNode(nodeOrType) {
    return getNodeRole(nodeOrType) === "output";
  }

  function hasBranchPorts(nodeOrType) {
    return isDecisionNode(nodeOrType) || isIntegrationNode(nodeOrType);
  }

  function nodeTypeFromResource(resourceType) {
    const type = String(resourceType || "restapi").toLowerCase();
    return nodeCatalog[type] ? type : "restapi";
  }

  function createEdge(sourceNodeId, sourcePort, targetNodeId, targetPort) {
    return {
      id: createId("edge"),
      sourceNodeId,
      sourcePort,
      targetNodeId,
      targetPort
    };
  }

  function renderApp() {
    renderStageTransform();
    renderNodes();
    renderEdges();
    renderInspector();
    renderTerminal();
    renderAppCode();
    updateStatus();
  }

  function renderUIState() {
    document.body.classList.toggle("left-collapsed", state.ui.leftCollapsed);
    document.body.classList.toggle("right-collapsed", state.ui.rightCollapsed);
    document.body.classList.toggle("bottom-collapsed", state.ui.bottomCollapsed);
    refs.bottomTabs.forEach((tab) => {
      const isActive = tab.dataset.bottomTab === state.ui.activeBottomTab;
      tab.classList.toggle("active", isActive);
      tab.setAttribute("aria-selected", String(isActive));
    });
    refs.bottomPanels.forEach((panel) => {
      panel.classList.toggle("active", panel.dataset.bottomPanel === state.ui.activeBottomTab);
    });
  }

  function renderStageTransform() {
    refs.workspaceStage.style.transform = `translate(${state.viewport.x}px, ${state.viewport.y}px) scale(${state.viewport.scale})`;
    refs.zoomLabel.textContent = `${Math.round(state.viewport.scale * 100)}%`;
  }

  function renderNodes() {
    refs.nodeLayer.innerHTML = "";

    state.nodes.forEach((node) => {
      const definition = nodeCatalog[node.type];
      const isExecuting = state.activeExecutionNodeIds.includes(node.id);
      const executionState = state.activeExecutionNodeStates[node.id] || "running";
      const nodeElement = document.createElement("article");
      nodeElement.className = `node-card ${definition.accentClass}${state.selectedNodeId === node.id ? " selected" : ""}${isExecuting ? ` executing execution-${executionState}` : ""}`;
      nodeElement.style.left = `${node.x}px`;
      nodeElement.style.top = `${node.y}px`;
      nodeElement.dataset.nodeId = node.id;

      const outputs = getOutputPorts(node);
      const inputs = getInputPorts(node);
      const iconClass = getIconClass(definition);

      nodeElement.innerHTML = `
        <div class="node-header" data-role="drag-handle">
          <span class="node-type-icon ${definition.accentClass}${iconClass}">${definition.icon}</span>
          <div class="node-title" data-role="title" title="Duplo clique para editar">${escapeHtml(formatNodeTitleForDisplay(node.title))}</div>
          <button class="node-kebab" type="button" data-role="menu" aria-label="Abrir menu">⋮</button>
        </div>
        <div class="node-body">
          <div class="node-type-label">
            <span>${definition.label}</span>
          </div>
          <p class="node-description">${escapeHtml(node.description || definition.description)}</p>
          ${renderNodeBody(node)}
        </div>
      `;

      nodeElement.addEventListener("pointerdown", (event) => {
        if (event.target.closest("[data-role='menu']") || event.target.closest(".port")) {
          return;
        }
        selectNode(node.id);
      });

      nodeElement.addEventListener("contextmenu", (event) => {
        event.preventDefault();
        event.stopPropagation();
        selectNode(node.id);
        openContextMenu(node.id, event.clientX, event.clientY);
      });

      const dragHandle = nodeElement.querySelector("[data-role='drag-handle']");
      dragHandle.addEventListener("pointerdown", (event) => startNodeDrag(event, node.id));

      const titleElement = nodeElement.querySelector("[data-role='title']");
      titleElement.addEventListener("dblclick", () => enableInlineTitleEdit(node.id, titleElement));

      nodeElement.querySelector("[data-role='menu']").addEventListener("click", (event) => {
        event.stopPropagation();
        openContextMenu(node.id, event.clientX, event.clientY);
      });

      appendPorts(nodeElement, inputs, "input");
      appendPorts(nodeElement, outputs, "output");
      refs.nodeLayer.appendChild(nodeElement);
    });
  }

  function renderNodeBody(node) {
    if (isDecisionNode(node)) {
      const conditionsMarkup = [
        { label: "Sucesso", expression: getConditionalExpression(node) },
        { label: "Falha", expression: "fallback" }
      ].map((condition) => `
        <div class="condition-row">
          <strong>${escapeHtml(condition.label)}</strong>
          <span>${escapeHtml(condition.expression)}</span>
        </div>
      `).join("");
      return `<div class="node-conditions">${conditionsMarkup}</div>`;
    }

    if (isIntegrationNode(node)) {
      return `
        <div class="node-footer">
          <div class="meta-chip">
            <strong>Bloco</strong>
            <span>${escapeHtml(node.metadata.source || "-")}</span>
          </div>
          <div class="meta-chip">
            <strong>Pacote</strong>
            <span>${escapeHtml(node.metadata.resourceType || "-")}</span>
          </div>
          <div class="meta-chip">
            <strong>Operação</strong>
            <span>${escapeHtml(node.metadata.operation || node.metadata.method || "-")}</span>
          </div>
        </div>
      `;
    }

    if (isOutputNode(node)) {
      return `
        <div class="node-footer">
          <div class="meta-chip">
            <strong>Output</strong>
            <span>${escapeHtml(node.metadata.aggregationName || "-")}</span>
          </div>
        </div>
      `;
    }

    if (isEntryNode(node)) {
      return `
      <div class="node-footer">
        <div class="meta-chip">
          <strong>Entrada</strong>
          <span>${escapeHtml(node.metadata.requestName || "entrypoint")}</span>
        </div>
        <div class="meta-chip">
          <strong>Rota</strong>
          <span>${escapeHtml(`${node.metadata.routeMethod || "POST"} ${node.metadata.routePath || "/"}`)}</span>
        </div>
        <div class="meta-chip">
          <strong>Runtime</strong>
          <span>${escapeHtml(node.metadata.serverKind || "HTTP")}</span>
        </div>
      </div>
    `;
    }

    return `
      <div class="node-footer">
        <div class="meta-chip">
          <strong>Construtor</strong>
          <span>${escapeHtml(node.metadata.operation || nodeCatalog[node.type].label)}</span>
        </div>
        <div class="meta-chip">
          <strong>State</strong>
          <span>${escapeHtml(node.metadata.stateKey || "-")}</span>
        </div>
      </div>
    `;
  }

  function appendPorts(nodeElement, ports, side) {
    const spacing = 100 / (ports.length + 1);
    ports.forEach((port, index) => {
      const topPercent = spacing * (index + 1);
      const isIconPort = ["✓", "✗", "→"].includes(port.label);
      const portButton = document.createElement("button");
      portButton.type = "button";
      portButton.className = `port port-${side}`;
      portButton.style.top = `calc(${topPercent}% - 10px)`;
      portButton.dataset.nodeId = nodeElement.dataset.nodeId;
      portButton.dataset.portKey = port.key;
      portButton.dataset.portSide = side;
      portButton.title = `${side === "input" ? "Entrada" : "Saida"}: ${port.description || port.label}`;
      if (isIconPort) {
        portButton.textContent = port.label;
        portButton.classList.add(`port-${port.key}`);
      }
      if (state.pendingConnection && state.pendingConnection.nodeId === nodeElement.dataset.nodeId && state.pendingConnection.portKey === port.key) {
        portButton.classList.add("pending");
      }
      portButton.addEventListener("click", (event) => {
        event.stopPropagation();
        handlePortClick(nodeElement.dataset.nodeId, port.key, side);
      });

      const label = document.createElement("span");
      label.className = `port-label ${side}`;
      label.style.top = `calc(${topPercent}% - 6px)`;
      label.textContent = port.label;

      nodeElement.appendChild(portButton);
      if (!isIconPort) {
        nodeElement.appendChild(label);
      }
    });
  }

  function renderEdges() {
    refs.edgeLayer.innerHTML = "";
    refs.edgeLayer.setAttribute("viewBox", `0 0 ${STAGE_WIDTH} ${STAGE_HEIGHT}`);
    refs.edgeLayer.setAttribute("width", STAGE_WIDTH);
    refs.edgeLayer.setAttribute("height", STAGE_HEIGHT);

    state.edges.forEach((edge) => {
      const sourcePoint = getPortPosition(edge.sourceNodeId, edge.sourcePort, "output");
      const targetPoint = getPortPosition(edge.targetNodeId, edge.targetPort, "input");
      if (!sourcePoint || !targetPoint) {
        return;
      }

      const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      path.setAttribute("class", `edge-path${isEdgeHighlighted(edge) ? " selected" : ""}`);
      path.setAttribute("d", createBezierPath(sourcePoint, targetPoint));
      refs.edgeLayer.appendChild(path);

      if (shouldRenderEdgeLabel(edge)) {
        const text = document.createElementNS("http://www.w3.org/2000/svg", "text");
        text.setAttribute("class", "edge-label");
        text.setAttribute("x", String((sourcePoint.x + targetPoint.x) / 2));
        text.setAttribute("y", String((sourcePoint.y + targetPoint.y) / 2 - 8));
        text.textContent = getEdgeLabel(edge);
        refs.edgeLayer.appendChild(text);
      }
    });

    if (state.pendingConnection && state.pendingConnection.pointer) {
      const sourcePoint = getPortPosition(state.pendingConnection.nodeId, state.pendingConnection.portKey, "output");
      if (sourcePoint) {
        const preview = document.createElementNS("http://www.w3.org/2000/svg", "path");
        preview.setAttribute("class", "edge-path temp-edge selected");
        preview.setAttribute("d", createBezierPath(sourcePoint, state.pendingConnection.pointer));
        refs.edgeLayer.appendChild(preview);
      }
    }
  }

  function renderInspector() {
    const node = getSelectedNode();
    if (!node) {
      refs.inspectorContent.className = "inspector-empty";
      refs.inspectorContent.innerHTML = "<p>Selecione um bloco no canvas para configurar o servico, o flow e as integracoes.</p>";
      return;
    }

    refs.inspectorContent.className = "";
    refs.inspectorContent.innerHTML = isEntryNode(node)
      ? renderRequestInspector(node)
      : `
      <div class="node-type-label">
        <span>${nodeCatalog[node.type].label}</span>
      </div>
      <form class="inspector-form" id="inspectorForm">
        <div class="field-group">
          <label for="field-nodeId">ID</label>
          <input id="field-nodeId" name="nodeId" type="text" value="${escapeAttribute(getNodeBusinessId(node))}">
        </div>
        <div class="field-group">
          <label for="field-type">Tipo de bloco</label>
          <select id="field-type" name="type">
            ${Object.entries(nodeCatalog).map(([type, definition]) => `
              <option value="${type}"${node.type === type ? " selected" : ""}>${definition.label}</option>
            `).join("")}
          </select>
        </div>
        <div class="field-group">
          <label for="field-title">Nome no codigo</label>
          <input id="field-title" name="title" type="text" value="${escapeAttribute(node.title)}">
        </div>
        <div class="field-group">
          <label for="field-description">Descricao</label>
          <textarea id="field-description" name="description">${escapeHtml(node.description || "")}</textarea>
        </div>
        ${renderSpecificFields(node)}
        <div class="inspector-actions">
          <button class="property-action" id="duplicateNodeBtn" type="button">Duplicar</button>
          <button class="property-action delete" id="deleteNodeBtn" type="button">Remover</button>
        </div>
      </form>
    `;

    bindInspectorEvents(node.id);
  }

  function renderRequestInspector(node) {
    return `
      <div class="node-type-label">
        <span>${nodeCatalog[node.type].label}</span>
      </div>
      <form class="inspector-form" id="inspectorForm">
        <div class="field-group">
          <label for="field-scenarioName">Nome da aplicacao</label>
          <input id="field-scenarioName" name="scenarioName" type="text" value="${escapeAttribute(node.metadata.scenarioName || "")}">
        </div>
        <div class="field-group">
          <label for="field-requestName">ID do server</label>
          <input id="field-requestName" name="requestName" type="text" value="${escapeAttribute(node.metadata.requestName || "")}">
        </div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-sigla">Sigla</label>
            <input id="field-sigla" name="sigla" type="text" value="${escapeAttribute(node.metadata.sigla || "")}">
          </div>
          <div class="field-group">
            <label for="field-siglaApp">Sigla APP</label>
            <input id="field-siglaApp" name="siglaApp" type="text" value="${escapeAttribute(node.metadata.siglaApp || "")}">
          </div>
        </div>
        <div class="field-group">
          <label for="field-type">Tipo de bloco</label>
          <select id="field-type" name="type">
            ${Object.entries(nodeCatalog).map(([type, definition]) => `
              <option value="${type}"${node.type === type ? " selected" : ""}>${definition.label}</option>
            `).join("")}
          </select>
        </div>
        <div class="field-group">
          <label for="field-title">Nome do servico</label>
          <input id="field-title" name="title" type="text" value="${escapeAttribute(node.title)}">
        </div>
        <div class="field-group">
          <label for="field-description">Descricao</label>
          <textarea id="field-description" name="description">${escapeHtml(node.description || "")}</textarea>
        </div>
        <div class="inspector-divider" aria-hidden="true"></div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-serverKind">Construtor</label>
            <select id="field-serverKind" name="serverKind">
              ${["HTTP", "Lambda", "TCP"].map((value) => `
                <option value="${value}"${(node.metadata.serverKind || "HTTP") === value ? " selected" : ""}>server.New${value}</option>
              `).join("")}
            </select>
          </div>
          <div class="field-group">
            <label for="field-port">WithPort</label>
            <input id="field-port" name="port" type="text" value="${escapeAttribute(node.metadata.port || "8080")}">
          </div>
        </div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-readTimeout">WithReadTimeout</label>
            <input id="field-readTimeout" name="readTimeout" type="text" value="${escapeAttribute(node.metadata.readTimeout || "30s")}">
          </div>
          <div class="field-group">
            <label for="field-writeTimeout">WithWriteTimeout</label>
            <input id="field-writeTimeout" name="writeTimeout" type="text" value="${escapeAttribute(node.metadata.writeTimeout || "30s")}">
          </div>
        </div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-idleTimeout">WithIdleTimeout</label>
            <input id="field-idleTimeout" name="idleTimeout" type="text" value="${escapeAttribute(node.metadata.idleTimeout || "60s")}">
          </div>
          <div class="field-group">
            <label for="field-shutdownTimeout">WithShutdownTimeout</label>
            <input id="field-shutdownTimeout" name="shutdownTimeout" type="text" value="${escapeAttribute(node.metadata.shutdownTimeout || "10s")}">
          </div>
        </div>
        <div class="field-group">
          <label for="field-source">WithSource</label>
          <select id="field-source" name="source">
            ${["SourceAPIGatewayV2", "SourceAPIGatewayV1", "SourceALB"].map((value) => `
              <option value="${value}"${(node.metadata.source || "SourceAPIGatewayV2") === value ? " selected" : ""}>server.${value}</option>
            `).join("")}
          </select>
          <p class="field-help">Usado quando o construtor for server.NewLambda.</p>
        </div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-routeMethod">Router method</label>
            <select id="field-routeMethod" name="routeMethod">
              ${["GET", "POST", "PUT", "PATCH", "DELETE"].map((method) => `
                <option value="${method}"${(node.metadata.routeMethod || "POST") === method ? " selected" : ""}>${method}</option>
              `).join("")}
            </select>
          </div>
          <div class="field-group">
            <label for="field-routePath">Router path</label>
            <input id="field-routePath" name="routePath" type="text" value="${escapeAttribute(node.metadata.routePath || "/")}">
          </div>
        </div>
        <div class="field-group">
          <label for="field-middleware">WithMiddleware</label>
          <input id="field-middleware" name="middleware" type="text" value="${escapeAttribute(node.metadata.middleware || "Logging, Recovery")}">
        </div>
        <div class="inspector-divider" aria-hidden="true"></div>
        <div class="field-group">
          <label for="field-payloadText">Payload de exemplo</label>
          <textarea id="field-payloadText" class="json-editor" data-json-field="payloadText" spellcheck="false">${escapeHtml(resolveRequestPayloadText(node))}</textarea>
          <p class="field-help">JSON usado para documentar o contrato inicial do handler.</p>
        </div>
        <div class="inspector-actions">
          <button class="property-action" id="duplicateNodeBtn" type="button">Duplicar</button>
          <button class="property-action delete" id="deleteNodeBtn" type="button">Remover</button>
        </div>
      </form>
    `;
  }

  function renderSpecificFields(node) {
    if (isEntryNode(node)) {
      return "";
    }

    if (isIntegrationNode(node)) {
      return `
        <div class="inspector-divider" aria-hidden="true"></div>
        <div class="property-section">
          <div class="property-grid">
            <div class="field-group">
          <label for="field-resourceType">Pacote / construtor</label>
              <select id="field-resourceType" name="resourceType">
                ${["dynamodb", "rds", "redis", "s3", "restapi", "parameterstore", "secretsmanager", "cnab"].map((value) => `
                  <option value="${value}"${node.metadata.resourceType === value ? " selected" : ""}>${value}</option>
                `).join("")}
              </select>
            </div>
            <div class="field-group">
              <label for="field-method">Metodo HTTP para output.REST</label>
              <select id="field-method" name="method">
                ${["GET", "POST", "PUT", "PATCH", "DELETE"].map((method) => `
                  <option value="${method}"${node.metadata.method === method ? " selected" : ""}>${method}</option>
                `).join("")}
              </select>
            </div>
          </div>
          ${renderIntegrationOperationField(node)}
          <div class="field-group">
            <label for="field-endpoint">Endpoint principal</label>
            <input id="field-endpoint" name="endpoint" type="text" value="${escapeAttribute(node.metadata.endpoint || "")}">
          </div>
          <div class="field-group">
            <label for="field-lookupKey">Input usado no StepFn</label>
            <input id="field-lookupKey" name="lookupKey" type="text" value="${escapeAttribute(node.metadata.lookupKey || "")}">
          </div>
          <div class="field-group">
            <label for="field-source">Nome da variavel / core.Block</label>
            <input id="field-source" name="source" type="text" value="${escapeAttribute(node.metadata.source || "")}">
          </div>
          ${renderIntegrationProperties(node)}
          <div class="field-group">
            <label for="field-mockText">Exemplo de retorno</label>
            <textarea id="field-mockText" class="json-editor" data-json-field="mockText" spellcheck="false">${escapeHtml(resolveFetchMockText(node))}</textarea>
            <p class="field-help">JSON opcional para documentar o state produzido por este step.</p>
          </div>
        </div>
        ${renderConditionalOutcomeFields(node, "success", "Sucesso")}
        ${renderConditionalOutcomeFields(node, "failure", "Falha")}
      `;
    }

    if (isOutputNode(node)) {
      return `
        <div class="field-group">
          <label for="field-aggregationName">Step name</label>
          <input id="field-aggregationName" name="aggregationName" type="text" value="${escapeAttribute(node.metadata.aggregationName || "")}">
        </div>
        <div class="field-group">
          <label for="field-operation">Construtor output</label>
          <select id="field-operation" name="operation">
            ${["JSON", "JSONFrom", "Created", "OK", "Text", "NoContent", "Redirect", "REST", "Call", "CallJSON"].map((value) => `
              <option value="${value}"${(node.metadata.operation || "JSON") === value ? " selected" : ""}>output.${value}</option>
            `).join("")}
          </select>
        </div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-responseStatus">statusCode</label>
            <input id="field-responseStatus" name="responseStatus" type="number" value="${escapeAttribute(node.metadata.responseStatus || "200")}">
          </div>
          <div class="field-group">
            <label for="field-stateKey">stateKey</label>
            <input id="field-stateKey" name="stateKey" type="text" value="${escapeAttribute(node.metadata.stateKey || "response")}">
          </div>
        </div>
        <div class="field-group">
          <label for="field-expression">Expressao completa</label>
          <textarea id="field-expression" name="expression">${escapeHtml(node.metadata.expression || "")}</textarea>
        </div>
      `;
    }

    if (isDecisionNode(node)) {
      return `
      <div class="inspector-divider" aria-hidden="true"></div>
      <div class="field-group">
        <label for="field-celExpression">Expressao CEL</label>
        <textarea id="field-celExpression" name="celExpression">${escapeHtml(getConditionalExpression(node))}</textarea>
      </div>
      <div class="field-group">
        <label for="field-ruleName">WithRule name</label>
        <input id="field-ruleName" name="ruleName" type="text" value="${escapeAttribute(node.metadata.ruleName || sanitizeStepId(node.title))}">
      </div>
      <div class="field-group">
        <label for="field-schemaText">decision.Schema</label>
        <textarea id="field-schemaText" name="schemaText">${escapeHtml(node.metadata.schemaText || "{\n  \"customer_type\": \"decision.String\"\n}")}</textarea>
      </div>
      ${renderConditionalOutcomeFields(node, "success", "Sucesso")}
      ${renderConditionalOutcomeFields(node, "failure", "Falha")}
    `;
    }

    if (getNodeRole(node) === "flow") {
      return `
        <div class="inspector-divider" aria-hidden="true"></div>
        <div class="field-group">
          <label for="field-stepId">flow.New name</label>
          <input id="field-stepId" name="stepId" type="text" value="${escapeAttribute(node.metadata.stepId || sanitizeStepId(node.title))}">
        </div>
        <div class="property-grid">
          <div class="field-group">
            <label for="field-handlerName">Handler</label>
            <input id="field-handlerName" name="handlerName" type="text" value="${escapeAttribute(node.metadata.handlerName || sanitizeStepId(node.title))}">
          </div>
          <div class="field-group">
            <label for="field-stateKey">State key</label>
            <input id="field-stateKey" name="stateKey" type="text" value="${escapeAttribute(node.metadata.stateKey || sanitizeStepId(node.title))}">
          </div>
        </div>
      `;
    }

    return `
      <div class="inspector-divider" aria-hidden="true"></div>
      <div class="field-group">
        <label for="field-operation">Transform constructor</label>
        <select id="field-operation" name="operation">
          ${["transform.New", "transform.Merge", "transform.Pick", "transform.Omit", "transform.Rename", "transform.Step"].map((value) => `
            <option value="${value}"${(node.metadata.operation || "transform.New") === value ? " selected" : ""}>${value}</option>
          `).join("")}
        </select>
      </div>
      <div class="field-group">
        <label for="field-stateKey">State key</label>
        <input id="field-stateKey" name="stateKey" type="text" value="${escapeAttribute(node.metadata.stateKey || sanitizeStepId(node.title))}">
      </div>
      <div class="field-group">
        <label for="field-expression">Expressao</label>
        <textarea id="field-expression" name="expression">${escapeHtml(node.metadata.expression || "")}</textarea>
      </div>
    `;
  }

  function renderConditionalOutcomeFields(node, outcome, label) {
    const action = node.metadata[`${outcome}Action`] || (outcome === "failure" ? "return_error" : "next_step");
    return `
      <div class="condition-editor-row">
        <div class="condition-editor-header">
          <strong>${label}</strong>
        </div>
        <div class="field-group">
          <label for="field-${outcome}Action">Acao</label>
          <select id="field-${outcome}Action" name="${outcome}Action">
            <option value="next_step"${action === "next_step" ? " selected" : ""}>next_step</option>
            <option value="return_success"${action === "return_success" ? " selected" : ""}>return_success</option>
            <option value="return_error"${action === "return_error" ? " selected" : ""}>return_error</option>
          </select>
        </div>
        ${action === "next_step" ? `
          <div class="field-group">
            <label for="field-${outcome}Target">Target</label>
            <input id="field-${outcome}Target" name="${outcome}Target" type="text" value="${escapeAttribute(node.metadata[`${outcome}Target`] || "")}">
            <p class="field-help">Se houver uma conexao visual neste ramo, ela sera usada como target.</p>
          </div>
        ` : `
          <div class="property-grid">
            <div class="field-group">
              <label for="field-${outcome}HttpStatus">HTTP status</label>
              <input id="field-${outcome}HttpStatus" name="${outcome}HttpStatus" type="number" value="${escapeAttribute(node.metadata[`${outcome}HttpStatus`] || (action === "return_success" ? "200" : "400"))}">
            </div>
            <div class="field-group">
              <label for="field-${outcome}Message">Message</label>
              <input id="field-${outcome}Message" name="${outcome}Message" type="text" value="${escapeAttribute(node.metadata[`${outcome}Message`] || "")}">
            </div>
          </div>
        `}
      </div>
    `;
  }

  function renderIntegrationProperties(node) {
    const pkg = node.metadata.resourceType || node.type;
    const field = (name, label, fallback = "", help = "") => `
      <div class="field-group">
        <label for="field-${name}">${label}</label>
        <input id="field-${name}" name="${name}" type="text" value="${escapeAttribute(node.metadata[name] || fallback)}">
        ${help ? `<p class="field-help">${escapeHtml(help)}</p>` : ""}
      </div>
    `;
    const select = (name, label, values, fallback = "") => `
      <div class="field-group">
        <label for="field-${name}">${label}</label>
        <select id="field-${name}" name="${name}">
          ${values.map((value) => `<option value="${value}"${(node.metadata[name] || fallback) === value ? " selected" : ""}>${value}</option>`).join("")}
        </select>
      </div>
    `;

    if (pkg === "dynamodb") {
      return `
        <div class="property-grid">
          ${field("region", "WithRegion", "us-east-1")}
          ${field("profile", "WithProfile", "")}
        </div>
        <div class="property-grid">
          ${field("endpointOverride", "WithEndpoint", "")}
          ${field("table", "WithTable", node.metadata.endpoint || "")}
        </div>
        <div class="property-grid">
          ${field("partitionKey", "WithPartitionKey", "id")}
          ${field("sortKey", "WithSortKey", "")}
        </div>
      `;
    }

    if (pkg === "rds") {
      return `
        <div class="property-grid">
          ${select("driver", "WithDriver", ["DriverPostgres", "DriverMySQL"], "DriverPostgres")}
          ${field("database", "WithDatabase", "app")}
        </div>
        ${field("dsn", "WithDSN", node.metadata.endpoint || "", "Pode apontar para Secrets Manager depois.")}
        <div class="property-grid">
          ${field("host", "WithHost", "localhost")}
          ${field("port", "WithPort", "5432")}
        </div>
        <div class="property-grid">
          ${field("username", "WithUsername", "app")}
          ${field("password", "WithPassword", "")}
        </div>
        ${select("sslMode", "WithSSLMode", ["require", "disable", "verify-ca", "verify-full"], "require")}
        <div class="property-grid">
          ${field("maxOpenConns", "WithMaxOpenConns", "10")}
          ${field("maxIdleConns", "WithMaxIdleConns", "5")}
        </div>
        <div class="property-grid">
          ${field("connMaxLifetime", "WithConnMaxLifetime", "5m")}
          ${field("connMaxIdleTime", "WithConnMaxIdleTime", "1m")}
        </div>
        <div class="property-grid">
          ${field("queryTimeout", "WithQueryTimeout", "30s")}
        </div>
      `;
    }

    if (pkg === "redis") {
      return `
        <div class="property-grid">
          ${field("addr", "WithAddr", node.metadata.endpoint || "localhost:6379")}
          ${field("password", "WithPassword", "")}
        </div>
        <div class="property-grid">
          ${field("db", "WithDB", "0")}
          ${field("keyPrefix", "WithKeyPrefix", "app:")}
        </div>
        <div class="property-grid">
          ${field("poolSize", "WithPoolSize", "10")}
          ${field("dialTimeout", "WithDialTimeout", "5s")}
        </div>
        <div class="property-grid">
          ${field("readTimeout", "WithReadTimeout", "3s")}
          ${field("writeTimeout", "WithWriteTimeout", "3s")}
        </div>
      `;
    }

    if (pkg === "s3") {
      return `
        <div class="property-grid">
          ${field("region", "WithRegion", "us-east-1")}
          ${field("profile", "WithProfile", "")}
        </div>
        <div class="property-grid">
          ${field("endpointOverride", "WithEndpoint", "")}
          ${field("bucket", "WithBucket", node.metadata.endpoint || "")}
        </div>
        <div class="property-grid">
          ${field("keyPrefix", "WithKeyPrefix", "")}
          ${select("pathStyle", "WithPathStyle", ["false", "true"], "false")}
        </div>
      `;
    }

    if (pkg === "parameterstore") {
      return `
        <div class="property-grid">
          ${field("region", "WithRegion", "us-east-1")}
          ${field("profile", "WithProfile", "")}
        </div>
        <div class="property-grid">
          ${field("endpointOverride", "WithEndpoint", "")}
          ${field("pathPrefix", "WithPathPrefix", node.metadata.endpoint || "/myapp/dev")}
        </div>
        ${select("withDecryption", "WithDecryption", ["true", "false"], "true")}
      `;
    }

    if (pkg === "secretsmanager") {
      return `
        <div class="property-grid">
          ${field("region", "WithRegion", "us-east-1")}
          ${field("profile", "WithProfile", "")}
        </div>
        <div class="property-grid">
          ${field("endpointOverride", "WithEndpoint", "")}
          ${field("versionStage", "WithVersionStage", "AWSCURRENT")}
        </div>
        ${field("secretName", "Secret usado no step", node.metadata.endpoint || "")}
      `;
    }

    if (pkg === "restapi") {
      return `
        ${field("baseURL", "WithBaseURL", node.metadata.endpoint || "https://api.example.com")}
        <div class="property-grid">
          ${field("timeout", "WithTimeout", "30s")}
          ${field("maxIdleConns", "WithMaxIdleConns", "10")}
        </div>
        <div class="property-grid">
          ${field("defaultHeaderKey", "WithHeader key", "Accept")}
          ${field("defaultHeaderValue", "WithHeader value", "application/json")}
        </div>
        <div class="property-grid">
          ${select("auth", "Auth option", ["none", "WithBearerToken", "WithBasicAuth", "WithOAuth2ClientCredentials", "WithAPIKeyHeader", "WithAPIKeyQuery", "WithTokenProvider"], "none")}
          ${field("authValue", "Auth value/provider", "")}
        </div>
      `;
    }

    if (pkg === "cnab") {
      return `
        <div class="property-grid">
          ${select("format", "WithFormat", ["Format240", "Format400"], "Format240")}
          ${select("skipUnknownSegments", "WithSkipUnknownSegments", ["false", "true"], "false")}
        </div>
        <div class="property-grid">
          ${field("dateLocation", "WithDateLocation", "UTC")}
          ${field("segmentCode", "WithSegment", "A")}
        </div>
        ${field("layoutFields", "Layout fields", "cnab.Field(\"nome\", 1, 10)")}
      `;
    }

    return "";
  }

  function renderIntegrationOperationField(node) {
    const operationsByPackage = {
      dynamodb: ["PutItem", "GetItem", "DeleteItem", "QueryItems", "ScanItems"],
      rds: ["QueryRows", "QueryOne", "QueryAll", "QueryPage", "Exec", "Tx", "Ping"],
      redis: ["Set", "Get", "SetJSON", "GetJSON", "Delete", "HSet", "HGet", "HGetAll"],
      s3: ["PutObject", "GetObject", "DeleteObject", "ListObjects"],
      parameterstore: ["GetParameter", "GetParametersByPath", "PutParameter"],
      secretsmanager: ["GetSecretJSON", "CreateSecret", "UpdateSecret", "RotateSecret"],
      restapi: ["Call", "CallJSON", "FanOut", "Pipeline"],
      cnab: ["Parse", "ParseFile"]
    };
    const pkg = node.metadata.resourceType || node.type;
    const operations = operationsByPackage[pkg] || ["Call"];
    const current = node.metadata.operation || operations[0];
    return `
      <div class="field-group">
        <label for="field-operation">Metodo usado no step</label>
        <select id="field-operation" name="operation">
          ${operations.map((operation) => `<option value="${operation}"${current === operation ? " selected" : ""}>${operation}</option>`).join("")}
        </select>
      </div>
    `;
  }

  function defaultOperationForPackage(packageName) {
    const defaults = {
      dynamodb: "GetItem",
      rds: "QueryOne",
      redis: "GetJSON",
      s3: "GetObject",
      parameterstore: "GetParameter",
      secretsmanager: "GetSecretJSON",
      restapi: "CallJSON",
      cnab: "Parse"
    };
    return defaults[packageName] || "Call";
  }

  function bindInspectorEvents(nodeId) {
    const form = document.getElementById("inspectorForm");
    if (!form) {
      return;
    }

    form.addEventListener("input", (event) => {
      const { name, value } = event.target;
      if (event.target.dataset.jsonField || !name || name === "type") {
        return;
      }
      updateNode(nodeId, (node) => {
        if (name === "title" || name === "description") {
          node[name] = value;
          if (isEntryNode(node) && name === "title") {
            node.metadata.serviceName = value;
            state.execution.serviceName = value;
            refs.serviceNameInput.value = value;
          }
          return;
        }
        if (isEntryNode(node) && name === "requestName") {
          node.metadata.requestName = value;
          return;
        }
        if (name === "nodeId") {
          setNodeBusinessId(node, value);
          return;
        }
        if (isDecisionNode(node) && name === "celExpression") {
          node.metadata.celExpression = value;
          ensureConditionalPorts(node);
          node.conditions[0].expression = value;
          return;
        }
        if (name === "resourceType") {
          node.metadata.resourceType = value;
          node.metadata.operation = defaultOperationForPackage(value);
          return;
        }
        node.metadata[name] = value;
      });
    });

    form.addEventListener("input", (event) => {
      const field = event.target.dataset.jsonField;
      if (!field) {
        return;
      }
      const node = getNodeById(nodeId);
      if (!node) {
        return;
      }
      node.metadata[field] = event.target.value;
      if (isEntryNode(node) && field === "payloadText") {
        state.execution.payloadText = event.target.value;
      }
      persistState("JSON de teste atualizado");
    });

    form.addEventListener("blur", (event) => {
      const field = event.target.dataset.jsonField;
      if (!field) {
        return;
      }
      try {
        JSON.parse(event.target.value || "{}");
        tryRefreshManifestPreview();
        renderNodes();
        updateStatus();
      } catch (error) {
        refs.storageStatus.textContent = `JSON invalido: ${error.message}`;
      }
    }, true);

    form.addEventListener("change", (event) => {
      const { name, value } = event.target;
      if (name === "type") {
        updateNode(nodeId, (node) => convertNodeType(node, value));
        return;
      }
      if (["successAction", "failureAction"].includes(name)) {
        updateNode(nodeId, (node) => {
          node.metadata[name] = value;
        });
      }
    });

    document.getElementById("duplicateNodeBtn").addEventListener("click", () => duplicateNode(nodeId));
    document.getElementById("deleteNodeBtn").addEventListener("click", () => deleteNode(nodeId));
  }

  function renderTerminal() {
    refs.terminal.innerHTML = state.execution.logs.length
      ? state.execution.logs.map((entry) => `
        <div class="log-line">
          <span class="log-time">${escapeHtml(entry.time)}</span>
          <span class="log-${entry.type}">[${escapeHtml(entry.type.toUpperCase())}]</span>
          ${escapeHtml(entry.message)}
        </div>
      `).join("")
      : `<div class="log-line"><span class="log-time">--:--:--</span> <span class="log-sys">[SYS]</span> Console pronto.</div>`;
    refs.terminal.scrollTop = refs.terminal.scrollHeight;
  }

  function renderAppCode() {
    if (!refs.appCode) {
      return;
    }
    try {
      const compiled = state.execution.manifestPreview
        ? { manifest: JSON.parse(state.execution.manifestPreview) }
        : compileWorkflowToManifest();
      if (compiled.manifest?.error) {
        throw new Error(compiled.manifest.error);
      }
      refs.appCode.textContent = generateGoSnippet(compiled.manifest);
    } catch (error) {
      refs.appCode.textContent = `// Ainda nao foi possivel gerar o codigo.\n// ${error.message}`;
    }
  }

  function updateStatus() {
    refs.nodeCount.textContent = `${state.nodes.length} blocos`;
    refs.edgeCount.textContent = `${state.edges.length} conexoes`;
    refs.executionStatus.textContent = state.execution.running ? "Gerando blueprint..." : "Gerador pronto";
  }

  function refreshManifestPreview() {
    try {
      const compiled = compileWorkflowToManifest();
      state.execution.manifestPreview = JSON.stringify(compiled.manifest, null, 2);
      state.execution.stepNodeMap = compiled.stepNodeMap;
      state.execution.terminalNodeMap = compiled.terminalNodeMap;
      persistState("Blueprint atualizado");
      return compiled;
    } catch (error) {
      state.execution.manifestPreview = JSON.stringify({ error: error.message }, null, 2);
      state.execution.stepNodeMap = {};
      state.execution.terminalNodeMap = {};
      persistState("Blueprint pendente");
      throw error;
    }
  }

  function tryRefreshManifestPreview() {
    try {
      return refreshManifestPreview();
    } catch (error) {
      return null;
    }
  }

  function addNodeToVisibleArea(type) {
    const viewportRect = refs.workspaceViewport.getBoundingClientRect();
    const worldPoint = screenToWorld({
      x: viewportRect.left + viewportRect.width * 0.38,
      y: viewportRect.top + viewportRect.height * 0.32
    });
    const node = createNode(type, { x: worldPoint.x, y: worldPoint.y });
    state.nodes.push(node);
    state.selectedNodeId = node.id;
    tryRefreshManifestPreview();
    renderApp();
    persistState("Bloco adicionado");
  }

  function selectNode(nodeId) {
    state.selectedNodeId = nodeId;
    renderNodes();
    renderEdges();
    renderInspector();
    updateStatus();
  }

  function getSelectedNode() {
    return state.nodes.find((node) => node.id === state.selectedNodeId) || null;
  }

  function updateNode(nodeId, updater) {
    const node = state.nodes.find((item) => item.id === nodeId);
    if (!node) {
      return;
    }
    updater(node);
    cleanupEdges();
    tryRefreshManifestPreview();
    renderApp();
    persistState("Alteracoes salvas");
  }

  function convertNodeType(node, newType) {
    if (!nodeCatalog[newType] || node.type === newType) {
      return;
    }
    const recreated = createNode(newType, {
      id: node.id,
      x: node.x,
      y: node.y,
      title: node.title,
      description: node.description
    });
    node.type = recreated.type;
    node.metadata = recreated.metadata;
    node.conditions = recreated.conditions;
  }

  function deleteNode(nodeId) {
    state.nodes = state.nodes.filter((node) => node.id !== nodeId);
    state.edges = state.edges.filter((edge) => edge.sourceNodeId !== nodeId && edge.targetNodeId !== nodeId);
    if (state.selectedNodeId === nodeId) {
      state.selectedNodeId = null;
    }
    cleanupEdges();
    hideContextMenu();
    tryRefreshManifestPreview();
    renderApp();
    persistState("Bloco removido");
  }

  function duplicateNode(nodeId) {
    const node = state.nodes.find((item) => item.id === nodeId);
    if (!node) {
      return;
    }
    const duplicated = clone(node);
    duplicated.id = createId("node");
    duplicated.x += 60;
    duplicated.y += 60;
    duplicated.title = `${node.title}Copy`;
    duplicated.conditions = Array.isArray(duplicated.conditions)
      ? duplicated.conditions.map((condition) => ({ ...condition, id: createId("cond") }))
      : [];
    state.nodes.push(duplicated);
    state.selectedNodeId = duplicated.id;
    hideContextMenu();
    tryRefreshManifestPreview();
    renderApp();
    persistState("Bloco duplicado");
  }

  function handlePortClick(nodeId, portKey, side) {
    hideContextMenu();
    selectNode(nodeId);

    if (side === "output") {
      state.pendingConnection = { nodeId, portKey, pointer: null };
      renderNodes();
      renderEdges();
      return;
    }

    if (!state.pendingConnection) {
      return;
    }

    if (state.pendingConnection.nodeId === nodeId) {
      state.pendingConnection = null;
      renderNodes();
      renderEdges();
      return;
    }

    const edgeExists = state.edges.some((edge) =>
      edge.sourceNodeId === state.pendingConnection.nodeId &&
      edge.sourcePort === state.pendingConnection.portKey &&
      edge.targetNodeId === nodeId &&
      edge.targetPort === portKey
    );

    if (!edgeExists) {
      state.edges.push(createEdge(state.pendingConnection.nodeId, state.pendingConnection.portKey, nodeId, portKey));
      tryRefreshManifestPreview();
      persistState("Conexao criada");
    }

    state.pendingConnection = null;
    cleanupEdges();
    renderApp();
  }

  function getInputPorts() {
    return [{ key: "in-main", label: "→", description: "Entrada" }];
  }

  function getOutputPorts(node) {
    if (isDecisionNode(node)) {
      return [
        { key: "success", label: "✓", description: "Sucesso" },
        { key: "failure", label: "✗", description: "Falha" }
      ];
    }
    if (isIntegrationNode(node)) {
      return [
        { key: "success", label: "✓", description: "Sucesso" },
        { key: "failure", label: "✗", description: "Falha" }
      ];
    }
    return [{ key: "out-main", label: "→", description: "Saida" }];
  }

  function getPortLabel(nodeId, portKey, side) {
    const node = state.nodes.find((item) => item.id === nodeId);
    if (!node) {
      return "";
    }
    const ports = side === "input" ? getInputPorts(node) : getOutputPorts(node);
    const match = ports.find((port) => port.key === portKey);
    return match ? match.label : "";
  }

  function getEdgeLabel(edge) {
    const sourceNode = state.nodes.find((node) => node.id === edge.sourceNodeId);
    if (!sourceNode) {
      return "";
    }
    if (isDecisionNode(sourceNode)) {
      return edge.sourcePort === "success" ? "true" : "false";
    }
    if (isIntegrationNode(sourceNode)) {
      return edge.sourcePort === "success" ? "Sucesso" : "Falha";
    }
    return getPortLabel(edge.sourceNodeId, edge.sourcePort, "output");
  }

  function getPortPosition(nodeId, portKey, side) {
    const selector = `.port[data-node-id="${nodeId}"][data-port-key="${portKey}"][data-port-side="${side}"]`;
    const element = refs.nodeLayer.querySelector(selector);
    if (!element) {
      return null;
    }

    const rect = element.getBoundingClientRect();
    const stageRect = refs.workspaceStage.getBoundingClientRect();
    return {
      x: (rect.left + rect.width / 2 - stageRect.left) / state.viewport.scale,
      y: (rect.top + rect.height / 2 - stageRect.top) / state.viewport.scale
    };
  }

  function createBezierPath(source, target) {
    const delta = Math.max(80, Math.abs(target.x - source.x) * 0.45);
    return `M ${source.x} ${source.y} C ${source.x + delta} ${source.y}, ${target.x - delta} ${target.y}, ${target.x} ${target.y}`;
  }

  function shouldRenderEdgeLabel(edge) {
    const sourceNode = state.nodes.find((node) => node.id === edge.sourceNodeId);
    return sourceNode && isDecisionNode(sourceNode);
  }

  function isEdgeHighlighted(edge) {
    return edge.sourceNodeId === state.selectedNodeId || edge.targetNodeId === state.selectedNodeId || state.activeExecutionNodeIds.includes(edge.sourceNodeId) || state.activeExecutionNodeIds.includes(edge.targetNodeId);
  }

  function startNodeDrag(event, nodeId) {
    if (event.target.closest("[data-role='menu']")) {
      return;
    }
    const node = state.nodes.find((item) => item.id === nodeId);
    if (!node) {
      return;
    }
    hideContextMenu();
    state.drag = {
      nodeId,
      pointerId: event.pointerId,
      offsetX: event.clientX,
      offsetY: event.clientY,
      startX: node.x,
      startY: node.y
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    selectNode(nodeId);
  }

  function handleSurfacePointerDown(event) {
    if (event.target.closest(".node-card") || event.target.closest(".context-menu")) {
      return;
    }

    hideContextMenu();
    if (state.pendingConnection) {
      state.pendingConnection = null;
      renderNodes();
      renderEdges();
    }

    if (event.button !== 0) {
      return;
    }

    state.pan = {
      pointerId: event.pointerId,
      startClientX: event.clientX,
      startClientY: event.clientY,
      startX: state.viewport.x,
      startY: state.viewport.y
    };
    refs.workspaceSurface.setPointerCapture(event.pointerId);
    state.selectedNodeId = null;
    renderApp();
  }

  function handlePointerMove(event) {
    if (state.drag) {
      const node = getNodeById(state.drag.nodeId);
      if (!node) {
        return;
      }
      const dx = (event.clientX - state.drag.offsetX) / state.viewport.scale;
      const dy = (event.clientY - state.drag.offsetY) / state.viewport.scale;
      node.x = state.drag.startX + dx;
      node.y = state.drag.startY + dy;
      renderNodes();
      renderEdges();
      updateStatus();
      return;
    }

    if (state.pan) {
      state.viewport.x = state.pan.startX + (event.clientX - state.pan.startClientX);
      state.viewport.y = state.pan.startY + (event.clientY - state.pan.startClientY);
      renderStageTransform();
      renderEdges();
      return;
    }

    if (state.pendingConnection) {
      state.pendingConnection.pointer = screenToWorld({ x: event.clientX, y: event.clientY });
      renderEdges();
    }
  }

  function handlePointerUp() {
    if (state.drag) {
      state.drag = null;
      tryRefreshManifestPreview();
      persistState("Posicao atualizada");
    }
    if (state.pan) {
      state.pan = null;
      persistState("Viewport atualizado");
    }
  }

  function handleWheel(event) {
    event.preventDefault();
    const direction = event.deltaY > 0 ? 0.92 : 1.08;
    zoomAroundClientPoint(clamp(state.viewport.scale * direction, 0.35, 1.8), event.clientX, event.clientY);
  }

  function zoomAroundClientPoint(nextScale, clientX, clientY) {
    const worldBefore = screenToWorld({ x: clientX, y: clientY });
    state.viewport.scale = nextScale;
    state.viewport.x = clientX - refs.workspaceViewport.getBoundingClientRect().left - worldBefore.x * nextScale;
    state.viewport.y = clientY - refs.workspaceViewport.getBoundingClientRect().top - worldBefore.y * nextScale;
    renderStageTransform();
    renderEdges();
    persistState("Zoom atualizado");
  }

  function setZoom(nextScale) {
    const viewportRect = refs.workspaceViewport.getBoundingClientRect();
    zoomAroundClientPoint(clamp(nextScale, 0.35, 1.8), viewportRect.left + viewportRect.width / 2, viewportRect.top + viewportRect.height / 2);
  }

  function screenToWorld(point) {
    const viewportRect = refs.workspaceViewport.getBoundingClientRect();
    return {
      x: (point.x - viewportRect.left - state.viewport.x) / state.viewport.scale,
      y: (point.y - viewportRect.top - state.viewport.y) / state.viewport.scale
    };
  }

  function fitView() {
    if (!state.nodes.length) {
      resetView();
      return;
    }
    const bounds = getNodesBounds(state.nodes);
    const viewportRect = refs.workspaceViewport.getBoundingClientRect();
    const padding = 140;
    const scaleX = (viewportRect.width - padding) / Math.max(bounds.width, 1);
    const scaleY = (viewportRect.height - padding) / Math.max(bounds.height, 1);
    state.viewport.scale = clamp(Math.min(scaleX, scaleY, 1), 0.35, 1.2);
    state.viewport.x = (viewportRect.width - bounds.width * state.viewport.scale) / 2 - bounds.minX * state.viewport.scale;
    state.viewport.y = (viewportRect.height - bounds.height * state.viewport.scale) / 2 - bounds.minY * state.viewport.scale;
    renderStageTransform();
    renderEdges();
    persistState("Fit view aplicado");
  }

  function centerSelection() {
    const node = getSelectedNode();
    if (!node) {
      return;
    }
    const viewportRect = refs.workspaceViewport.getBoundingClientRect();
    state.viewport.x = viewportRect.width / 2 - (node.x + node.width / 2) * state.viewport.scale;
    state.viewport.y = viewportRect.height / 2 - (node.y + node.height / 2) * state.viewport.scale;
    renderStageTransform();
    renderEdges();
    persistState("Selecao centralizada");
  }

  function resetView() {
    state.viewport = { x: 200, y: 120, scale: 1 };
    renderStageTransform();
    renderEdges();
    persistState("Viewport resetado");
  }

  function exportWorkflow() {
    const data = serializeState();
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "go-code-blocks-app.json";
    link.click();
    URL.revokeObjectURL(url);
  }

  function importWorkflow(event) {
    const [file] = event.target.files || [];
    if (!file) {
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const parsed = JSON.parse(reader.result);
        const importedState = normalizeImportedWorkflow(parsed);
        applyStoredState(importedState);
        tryRefreshManifestPreview();
        renderUIState();
        renderApp();
        fitView();
        logMessage(importedState.importSummary || "Projeto importado com sucesso.", "ok");
      } catch (error) {
        window.alert(`Nao foi possivel importar o JSON do projeto: ${error.message}`);
      } finally {
        refs.importInput.value = "";
      }
    };
    reader.readAsText(file);
  }

  function normalizeImportedWorkflow(data) {
    if (data && Array.isArray(data.nodes) && Array.isArray(data.edges)) {
      return {
        ...data,
        importSummary: "Projeto visual importado com sucesso."
      };
    }

    const extracted = extractArchitectureManifest(data);
    if (!extracted) {
      throw new Error("Formato nao suportado. Use um projeto exportado pelo editor ou um manifest com execution_steps.");
    }

    return buildWorkflowStateFromManifest(extracted);
  }

  function extractArchitectureManifest(data) {
    if (!data) {
      return null;
    }

    if (Array.isArray(data)) {
      const [scenario] = data;
      const manifest = scenario?.output?.architecture_manifest;
      if (!manifest || !Array.isArray(manifest.execution_steps)) {
        return null;
      }
      const scenarioLabel = scenario.name || `cenario ${scenario.id || 1}`;
      return {
        manifest,
        mocks: extractManifestMocks(manifest, scenario),
        payload: extractManifestPayload(manifest, scenario),
        request: extractRequestMetadata(manifest, scenario),
        summary: data.length > 1
          ? `Manifest importado a partir do ${scenarioLabel}.`
          : `Manifest importado a partir do ${scenarioLabel}.`
      };
    }

    if (Array.isArray(data.execution_steps)) {
      return {
        manifest: data,
        mocks: extractManifestMocks(data),
        payload: extractManifestPayload(data),
        request: extractRequestMetadata(data),
        summary: "Manifest de arquitetura importado com sucesso."
      };
    }

    if (Array.isArray(data.output?.architecture_manifest?.execution_steps)) {
      return {
        manifest: data.output.architecture_manifest,
        mocks: extractManifestMocks(data.output.architecture_manifest, data),
        payload: extractManifestPayload(data.output.architecture_manifest, data),
        request: extractRequestMetadata(data.output.architecture_manifest, data),
        summary: "Manifest importado a partir do campo output.architecture_manifest."
      };
    }

    if (Array.isArray(data.architecture_manifest?.execution_steps)) {
      return {
        manifest: data.architecture_manifest,
        mocks: extractManifestMocks(data.architecture_manifest, data),
        payload: extractManifestPayload(data.architecture_manifest, data),
        request: extractRequestMetadata(data.architecture_manifest, data),
        summary: "Manifest importado a partir do campo architecture_manifest."
      };
    }

    if (Array.isArray(data.manifest?.execution_steps)) {
      return {
        manifest: data.manifest,
        mocks: extractManifestMocks(data.manifest, data),
        payload: extractManifestPayload(data.manifest, data),
        request: extractRequestMetadata(data.manifest, data),
        summary: "Manifest importado a partir do campo manifest."
      };
    }

    return null;
  }

  function extractManifestMocks(manifest, container = {}) {
    return manifest?.["infra-mocks-format"] ||
      manifest?.infra_mocks_format ||
      container?.["infra-mocks-format"] ||
      container?.infra_mocks_format ||
      container?.output?.["infra-mocks-format"] ||
      container?.output?.infra_mocks_format ||
      container?.execution?.mocks ||
      findFirstValueByKeys(container, ["infra-mocks-format", "infra_mocks_format"]) ||
      {};
  }

  function extractManifestPayload(manifest, container = {}) {
    return manifest?.["request-payload-format"] ||
      manifest?.request_payload_format ||
      container?.["request-payload-format"] ||
      container?.request_payload_format ||
      container?.output?.["request-payload-format"] ||
      container?.output?.request_payload_format ||
      container?.execution?.payload ||
      findFirstValueByKeys(container, ["request-payload-format", "request_payload_format"]) ||
      defaultExecutionPayload();
  }

  function extractRequestMetadata(manifest, container = {}) {
    const request = manifest?.request ||
      manifest?.entrypoint ||
      container?.request ||
      container?.output?.request ||
      {};
    const id = request.id ||
      request.request_id ||
      container?.id ||
      manifest?.request_id ||
      manifest?.service_name ||
      "request";
    const title = request.title || request.name || container?.name || manifest?.title || container?.title || "";
    const scenarioName = container?.name || request.scenario_name || request.scenarioName || manifest?.scenario_name || manifest?.scenarioName || "";
    const serviceName = manifest?.service_name || request.service_name || request.serviceName || title || "";
    const description = request.details ??
      request.detail ??
      manifest?.details ??
      manifest?.detail ??
      container?.details ??
      container?.detail ??
      "";

    return {
      id: String(id),
      title: serviceName ? String(serviceName) : "",
      scenarioName: scenarioName ? String(scenarioName) : "",
      sigla: manifest?.sigla ? String(manifest.sigla) : "",
      siglaApp: manifest?.sigla_app || manifest?.siglaApp ? String(manifest.sigla_app || manifest.siglaApp) : "",
      serviceName: serviceName ? String(serviceName) : "",
      description: description ? String(description) : ""
    };
  }

  function findFirstValueByKeys(value, keys) {
    if (!value || typeof value !== "object") {
      return null;
    }

    for (const key of keys) {
      if (Object.prototype.hasOwnProperty.call(value, key)) {
        return value[key];
      }
    }

    const children = Array.isArray(value) ? value : Object.values(value);
    for (const child of children) {
      const found = findFirstValueByKeys(child, keys);
      if (found !== null && found !== undefined) {
        return found;
      }
    }

    return null;
  }

  function buildWorkflowStateFromManifest(imported) {
    const manifest = imported.manifest || {};
    const importedMocks = isPlainObject(imported.mocks) ? imported.mocks : {};
    const importedPayload = imported.payload !== undefined ? imported.payload : defaultExecutionPayload();
    const requestMetadata = imported.request || extractRequestMetadata(manifest);
    const requestId = requestMetadata.id || manifest.service_name || "request";
    const rawSteps = Array.isArray(manifest.execution_steps) ? manifest.execution_steps : [];
    if (!rawSteps.length) {
      throw new Error("O manifest nao possui execution_steps.");
    }

    const requestNode = createNode("server", {
      id: createId("node"),
      x: 120,
      y: 140,
      title: requestMetadata.title || titleFromRequestId(requestId),
      description: requestMetadata.description || "",
      metadata: {
        requestName: requestId,
        scenarioName: requestMetadata.scenarioName || "",
        sigla: requestMetadata.sigla || "",
        siglaApp: requestMetadata.siglaApp || "",
        serviceName: requestMetadata.serviceName || requestMetadata.title || "",
        payloadText: formatJsonForEditor(importedPayload)
      }
    });

    const steps = rawSteps.map((step, index) => {
      const stepId = String(step.step_id || `step_${index + 1}`);
      const isConditional = step.type === "condition" || step.type === "decision";
      return {
        raw: step,
        stepId,
        node: createNode(isConditional ? "decision" : nodeTypeFromResource(step.resource_type), {
          id: createId("node"),
          title: step.title || step.name || titleFromRequestId(stepId),
          description: importedStepDescription(step),
          metadata: isConditional
            ? {
              strategy: "first-match",
              stepId,
              celExpression: step.cel_expression || "true",
              successAction: step.on_success?.action || "next_step",
              successTarget: step.on_success?.target || "",
              successHttpStatus: String(step.on_success?.http_status || 200),
              successMessage: step.on_success?.message || "",
              failureAction: step.on_failure?.action || "return_error",
              failureTarget: step.on_failure?.target || "",
              failureHttpStatus: String(step.on_failure?.http_status || 400),
              failureMessage: step.on_failure?.message || ""
            }
            : {
              stepId,
              endpoint: step.source || "",
              method: "POST",
              resourceName: step.source || stepId,
              source: step.source || stepId,
              resourceType: step.resource_type || "restapi",
              operation: step.operation || "",
              endpoint: step.endpoint || step.source || "",
              lookupKey: step.lookup_key || "payload.id",
              mockText: formatJsonForEditor(importedMocks[step.source || stepId] || {}),
              successAction: step.on_success?.action || "next_step",
              successTarget: step.on_success?.target || "",
              successHttpStatus: String(step.on_success?.http_status || 200),
              successMessage: step.on_success?.message || "",
              failureAction: step.on_failure?.action || "return_error",
              failureTarget: step.on_failure?.target || "",
              failureHttpStatus: String(step.on_failure?.http_status || 500),
              failureMessage: step.on_failure?.message || `Falha em ${stepId}`
            },
          conditions: isConditional
            ? [
              { id: "success", label: "Sucesso", expression: step.cel_expression || "true" },
              { id: "failure", label: "Falha", expression: "fallback" }
            ]
            : undefined
        })
      };
    });

    const stepNodeMap = new Map(steps.map((entry) => [entry.stepId, entry.node]));
    const nodeOrderMap = new Map(steps.map((entry, index) => [entry.stepId, index]));
    const transitionEdges = [];
    const terminalTargets = new Set();
    const nodes = [requestNode, ...steps.map((entry) => entry.node)];
    const edges = [];

    steps.forEach((entry) => {
      const { raw, stepId, node } = entry;
      const successTargetNode = resolveManifestTargetNode(stepNodeMap, raw.on_success?.action, raw.on_success?.target);
      const failureTargetNode = resolveManifestTargetNode(stepNodeMap, raw.on_failure?.action, raw.on_failure?.target);

      if (successTargetNode) {
        edges.push(createEdge(node.id, hasBranchPorts(node) ? "success" : "out-main", successTargetNode.id, "in-main"));
        transitionEdges.push([stepId, String(raw.on_success.target)]);
      } else if (raw.on_success?.action === "next_step" && raw.on_success?.target) {
        terminalTargets.add(String(raw.on_success.target));
      }

      if (hasBranchPorts(node)) {
        if (failureTargetNode) {
          edges.push(createEdge(node.id, "failure", failureTargetNode.id, "in-main"));
          transitionEdges.push([stepId, String(raw.on_failure.target)]);
        } else if (raw.on_failure?.action === "next_step" && raw.on_failure?.target) {
          terminalTargets.add(String(raw.on_failure.target));
        }
      }
    });

    let aggregatorNode = null;
    if (terminalTargets.size) {
      const aggregatorTitle = terminalTargets.size === 1
        ? [...terminalTargets][0]
        : "WorkflowCompleted";
      aggregatorNode = createNode("output", {
        id: createId("node"),
        title: aggregatorTitle,
        description: "Saida final importada do manifest.",
        metadata: {
          aggregationName: aggregatorTitle,
          expression: "imported_terminal"
        }
      });
      nodes.push(aggregatorNode);

      steps.forEach((entry) => {
        const { raw, node } = entry;
        if (raw.on_success?.action === "next_step" && raw.on_success?.target && !stepNodeMap.has(String(raw.on_success.target))) {
          edges.push(createEdge(node.id, hasBranchPorts(node) ? "success" : "out-main", aggregatorNode.id, "in-main"));
        }
        if (hasBranchPorts(node) && raw.on_failure?.action === "next_step" && raw.on_failure?.target && !stepNodeMap.has(String(raw.on_failure.target))) {
          edges.push(createEdge(node.id, "failure", aggregatorNode.id, "in-main"));
        }
      });
    }

    const firstStepNode = stepNodeMap.get(steps[0].stepId);
    if (!firstStepNode) {
      throw new Error("Nao foi possivel identificar o primeiro passo do manifest.");
    }

    edges.unshift(createEdge(requestNode.id, "out-main", firstStepNode.id, "in-main"));
    applyImportedLayout(requestNode, steps, aggregatorNode, transitionEdges, nodeOrderMap);

    return {
      nodes,
      edges,
      viewport: { x: 200, y: 120, scale: 1 },
      ui: clone(state.ui),
      execution: {
        serviceName: manifest.service_name || "orders-api",
        mocksText: formatJsonForEditor(importedMocks),
        payloadText: formatJsonForEditor(importedPayload),
        logs: []
      },
      importSummary: imported.summary
    };
  }

  function resolveManifestTargetNode(stepNodeMap, action, target) {
    if (action !== "next_step" || !target) {
      return null;
    }
    return stepNodeMap.get(String(target)) || null;
  }

  function importedStepDescription(step) {
    if (step.type === "condition" || step.type === "decision") {
      return "Decisao importada do blueprint.";
    }
    return `Integracao ${step.resource_type || "restapi"} importada do blueprint.`;
  }

  function applyImportedLayout(requestNode, importedSteps, aggregatorNode, transitionEdges, nodeOrderMap) {
    const depthMap = new Map([[importedSteps[0].stepId, 1]]);
    let changed = true;

    while (changed) {
      changed = false;
      transitionEdges.forEach(([from, to]) => {
        const fromDepth = depthMap.get(from);
        if (fromDepth == null) {
          return;
        }
        const nextDepth = fromDepth + 1;
        if ((depthMap.get(to) || 0) < nextDepth) {
          depthMap.set(to, nextDepth);
          changed = true;
        }
      });
    }

    const levelMap = new Map();
    importedSteps.forEach((entry) => {
      const depth = depthMap.get(entry.stepId) || 1;
      if (!levelMap.has(depth)) {
        levelMap.set(depth, []);
      }
      levelMap.get(depth).push(entry);
    });

    requestNode.x = 120;
    requestNode.y = 140;

    [...levelMap.entries()]
      .sort((a, b) => a[0] - b[0])
      .forEach(([depth, levelEntries]) => {
        levelEntries
          .sort((a, b) => (nodeOrderMap.get(a.stepId) || 0) - (nodeOrderMap.get(b.stepId) || 0))
          .forEach((entry, index) => {
            entry.node.x = 120 + depth * 360;
            entry.node.y = 80 + index * 240;
          });
      });

    if (aggregatorNode) {
      const maxDepth = Math.max(...depthMap.values());
      const lastLevel = levelMap.get(maxDepth) || [];
      const averageY = lastLevel.length
        ? lastLevel.reduce((sum, entry) => sum + entry.node.y, 0) / lastLevel.length
        : 160;
      aggregatorNode.x = 120 + (maxDepth + 1) * 360;
      aggregatorNode.y = averageY;
    }
  }

  function serializeState() {
    return {
      nodes: clone(state.nodes),
      edges: clone(state.edges),
      viewport: clone(state.viewport),
      ui: clone(state.ui),
      execution: {
        serviceName: state.execution.serviceName,
        mocksText: state.execution.mocksText,
        payloadText: state.execution.payloadText,
        manifestPreview: state.execution.manifestPreview,
        logs: state.execution.logs.slice(-200)
      }
    };
  }

  function applyStoredState(data) {
    if (data && Array.isArray(data.nodes) && Array.isArray(data.edges)) {
      state.nodes = data.nodes.map(sanitizeNode);
      state.edges = data.edges.map(sanitizeEdge).filter(Boolean);
      state.viewport = sanitizeViewport(data.viewport || state.viewport);
      state.selectedNodeId = state.nodes[0] ? state.nodes[0].id : null;
      state.pendingConnection = null;
    } else {
      loadExampleWorkflow();
    }

    state.ui = {
      leftCollapsed: Boolean(data?.ui?.leftCollapsed),
      rightCollapsed: Boolean(data?.ui?.rightCollapsed),
      bottomCollapsed: Boolean(data?.ui?.bottomCollapsed),
      activeBottomTab: data?.ui?.activeBottomTab === "logs" ? "logs" : "code"
    };

    if (data?.execution) {
      state.execution.serviceName = data.execution.serviceName || "orders-api";
      state.execution.mocksText = data.execution.mocksText || formatJsonForEditor(defaultExecutionMocks());
      state.execution.payloadText = data.execution.payloadText || formatJsonForEditor(defaultExecutionPayload());
      state.execution.logs = Array.isArray(data.execution.logs) ? data.execution.logs : [];
    } else {
      initializeExecutionDefaults();
    }

    hydrateNodeTestDataFromExecution();
    cleanupEdges();
    refs.serviceNameInput.value = state.execution.serviceName;
  }

  function sanitizeNode(node) {
    const type = normalizeNodeType(node);
    return createNode(type, {
      id: node.id || createId("node"),
      x: Number(node.x) || 0,
      y: Number(node.y) || 0,
      width: Number(node.width) || DEFAULT_NODE_SIZE.width,
      height: Number(node.height) || DEFAULT_NODE_SIZE.height,
      title: node.title || `${type}Step`,
      description: node.description || "",
      metadata: typeof node.metadata === "object" && node.metadata ? node.metadata : {},
      conditions: Array.isArray(node.conditions) ? node.conditions.map((condition) => ({
        id: condition.id || createId("cond"),
        label: condition.label || "else if",
        expression: condition.expression || ""
      })) : undefined
    });
  }

  function normalizeNodeType(node) {
    if (nodeCatalog[node?.type]) {
      return node.type;
    }
    if (node?.type === "request") {
      return "server";
    }
    if (node?.type === "conditional") {
      return "decision";
    }
    if (node?.type === "aggregator") {
      return "output";
    }
    if (node?.type === "fetch") {
      return nodeTypeFromResource(node?.metadata?.resourceType);
    }
    return "restapi";
  }

  function sanitizeEdge(edge) {
    if (!edge || !edge.sourceNodeId || !edge.targetNodeId) {
      return null;
    }
    return {
      id: edge.id || createId("edge"),
      sourceNodeId: edge.sourceNodeId,
      sourcePort: edge.sourcePort || "out-main",
      targetNodeId: edge.targetNodeId,
      targetPort: edge.targetPort || "in-main"
    };
  }

  function sanitizeViewport(viewport) {
    return {
      x: Number(viewport.x) || 200,
      y: Number(viewport.y) || 120,
      scale: clamp(Number(viewport.scale) || 1, 0.35, 1.8)
    };
  }

  function cleanupEdges() {
    state.edges = state.edges.filter((edge) => {
      const sourceNode = getNodeById(edge.sourceNodeId);
      const targetNode = getNodeById(edge.targetNodeId);
      if (!sourceNode || !targetNode) {
        return false;
      }
      const sourceExists = getOutputPorts(sourceNode).some((port) => port.key === edge.sourcePort);
      const targetExists = getInputPorts(targetNode).some((port) => port.key === edge.targetPort);
      return sourceExists && targetExists;
    });
  }

  function persistState(message) {
    try {
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(serializeState()));
      refs.storageStatus.textContent = message || "Projeto salvo localmente";
    } catch (error) {
      refs.storageStatus.textContent = "Erro ao salvar localmente";
    }
  }

  function loadFromStorage() {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY);
      return raw ? JSON.parse(raw) : null;
    } catch (error) {
      refs.storageStatus.textContent = "Salvamento local indisponivel";
      return null;
    }
  }

  function enableInlineTitleEdit(nodeId, element) {
    element.contentEditable = "true";
    element.focus();
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(element);
    selection.removeAllRanges();
    selection.addRange(range);

    const finalize = () => {
      element.contentEditable = "false";
      updateNode(nodeId, (node) => {
        const text = element.textContent.trim();
        node.title = text || node.title;
      });
      element.removeEventListener("blur", finalize);
      element.removeEventListener("keydown", handleInlineKeydown);
    };

    const handleInlineKeydown = (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        finalize();
      }
      if (event.key === "Escape") {
        event.preventDefault();
        element.textContent = getNodeById(nodeId).title;
        finalize();
      }
    };

    element.addEventListener("blur", finalize);
    element.addEventListener("keydown", handleInlineKeydown);
  }

  function handleKeydown(event) {
    if ((event.key === "Delete" || event.key === "Backspace") && !state.execution.running) {
      const tagName = document.activeElement && document.activeElement.tagName;
      if (tagName === "INPUT" || tagName === "TEXTAREA") {
        return;
      }
      if (state.selectedNodeId) {
        deleteNode(state.selectedNodeId);
      }
    }
    if (event.key === "Escape" && state.pendingConnection) {
      state.pendingConnection = null;
      renderNodes();
      renderEdges();
    }
  }

  function togglePanel(key) {
    state.ui[key] = !state.ui[key];
    renderUIState();
    renderEdges();
    persistState("Layout atualizado");
  }

  function setBottomTab(tabName) {
    state.ui.activeBottomTab = tabName === "logs" ? "logs" : "code";
    renderUIState();
    persistState("Painel atualizado");
  }

  function openContextMenu(nodeId, clientX, clientY) {
    state.contextNodeId = nodeId;
    refs.contextMenu.classList.remove("hidden");
    refs.contextMenu.style.left = `${clientX}px`;
    refs.contextMenu.style.top = `${clientY}px`;
  }

  function hideContextMenu() {
    refs.contextMenu.classList.add("hidden");
    state.contextNodeId = null;
  }

  function handleContextMenuAction(event) {
    const action = event.target.dataset.action;
    if (!action || !state.contextNodeId) {
      return;
    }
    if (action === "duplicate") {
      duplicateNode(state.contextNodeId);
    }
    if (action === "delete") {
      deleteNode(state.contextNodeId);
    }
    hideContextMenu();
  }

  function logMessage(message, type = "sys") {
    const time = new Date().toLocaleTimeString("pt-BR");
    state.execution.logs.push({ time, message, type });
    state.execution.logs = state.execution.logs.slice(-300);
    renderTerminal();
    persistState(message);
  }

  function clearTerminal() {
    state.execution.logs = [];
    renderTerminal();
    persistState("Terminal limpo");
  }

  async function runWorkflow() {
    if (state.execution.running) {
      return;
    }

    let compiled;

    try {
      compiled = refreshManifestPreview();
    } catch (error) {
      logMessage(`Nao foi possivel gerar o blueprint: ${error.message}`, "err");
      return;
    }

    state.execution.running = true;
    state.activeExecutionNodeIds = [];
    state.activeExecutionNodeStates = {};
    updateStatus();
    logMessage("Atualizando aplicacao Go em construcao.", "sys");

    try {
      await playDesignTrace(compiled);
      renderAppCode();
      setBottomTab("code");
      logMessage("Codigo Go atualizado na aba de aplicacao.", "ok");
    } catch (error) {
      logMessage(`Falha ao gerar blueprint: ${error.message}`, "err");
    } finally {
      state.execution.running = false;
      state.activeExecutionNodeIds = [];
      state.activeExecutionNodeStates = {};
      renderNodes();
      updateStatus();
    }
  }

  async function playDesignTrace(compiled) {
    const orderedNodeIds = [
      ...new Set([
        ...Object.values(compiled.stepNodeMap || {}),
        ...Object.values(compiled.terminalNodeMap || {})
      ])
    ];
    const requestNode = state.nodes.find(isEntryNode);
    if (requestNode) {
      orderedNodeIds.unshift(requestNode.id);
    }

    for (const nodeId of orderedNodeIds) {
      const node = getNodeById(nodeId);
      if (!node) {
        continue;
      }
      highlightExecutionNodes([node.id], "running");
      logMessage(`[Design] ${nodeCatalog[node.type].label}: ${node.title}`, "sys");
      await wait(220);
      highlightExecutionNodes([node.id], "success");
      await wait(280);
    }
    highlightExecutionNodes([]);
  }

  function highlightExecutionNodes(nodeIds, executionState = "running") {
    state.activeExecutionNodeIds = nodeIds.filter(Boolean);
    state.activeExecutionNodeStates = Object.fromEntries(state.activeExecutionNodeIds.map((nodeId) => [nodeId, executionState]));
    renderNodes();
    renderEdges();
  }

  function resolveAPIBaseURL() {
    if (window.GO_CODE_BLOCKS_STUDIO_CONFIG && window.GO_CODE_BLOCKS_STUDIO_CONFIG.apiBaseUrl) {
      return String(window.GO_CODE_BLOCKS_STUDIO_CONFIG.apiBaseUrl).replace(/\/$/, "");
    }

    if (window.WORKFLOW_STUDIO_CONFIG && window.WORKFLOW_STUDIO_CONFIG.apiBaseUrl) {
      return String(window.WORKFLOW_STUDIO_CONFIG.apiBaseUrl).replace(/\/$/, "");
    }

    if (window.SIMULATOR_API_BASE_URL) {
      return window.SIMULATOR_API_BASE_URL.replace(/\/$/, "");
    }

    const params = new URLSearchParams(window.location.search);
    const apiParam = params.get("apiBaseUrl");
    if (apiParam) {
      return apiParam.replace(/\/$/, "");
    }

    const { protocol, hostname, port, origin } = window.location;
    if (port === "4200" || port === "3000" || port === "5173" || port === "5500") {
      return `${protocol}//${hostname}:8080`;
    }
    return origin;
  }

  function resolveEffectiveAPIBaseURL() {
    return resolveAPIBaseURL().replace(/\/$/, "");
  }

  function resetToExample() {
    loadExampleWorkflow();
    initializeExecutionDefaults();
    refs.serviceNameInput.value = state.execution.serviceName;
    tryRefreshManifestPreview();
    renderApp();
    fitView();
    logMessage("Fluxo de exemplo restaurado.", "ok");
  }

  function compileWorkflowToManifest() {
    const requestNode = state.nodes.find(isEntryNode) || getLeftMostNode();
    if (!requestNode) {
      throw new Error("Nenhum bloco disponivel para compilar.");
    }

    const requestTargets = getOutgoingTargets(requestNode.id);
    const aggregatorNode = state.nodes.find(isOutputNode) || null;
    const terminalLabel = aggregatorNode ? sanitizeStepId(aggregatorNode.title) : "WorkflowCompleted";
    const stepNodeMap = {};
    const terminalNodeMap = aggregatorNode ? { [terminalLabel]: aggregatorNode.id } : {};
    const steps = [];
    const directSteps = requestTargets
      .map((node) => ({ node, weight: isDecisionNode(node) ? 2 : 1 }))
      .sort((a, b) => a.weight - b.weight || a.node.x - b.node.x || a.node.y - b.node.y)
      .map((item) => item.node);

    const branchTerminals = [];
    directSteps.forEach((targetNode, index) => {
      const nextStartNode = directSteps[index + 1] || null;
      const result = compileNodeRecursive(targetNode, {
        suffix: `root${index + 1}`,
        fallbackTarget: nextStartNode ? null : terminalLabel,
        terminalLabel,
        stepNodeMap,
        terminalNodeMap,
        pathVisited: new Set()
      });

      if (!result.entryTarget) {
        throw new Error(`Nao foi possivel compilar o ramo iniciado por ${targetNode.title}.`);
      }

      steps.push(...result.steps);
      if (nextStartNode && result.openTerminals.length) {
        result.openTerminals.forEach((terminalStepId) => patchStepTarget(steps, terminalStepId, sanitizeStepId(nextStartNode.title)));
      } else {
        branchTerminals.push(...result.openTerminals);
      }
    });

    branchTerminals.forEach((terminalStepId) => patchStepTarget(steps, terminalStepId, terminalLabel));

    const payload = buildExecutionPayload();
    const mocks = buildExecutionMocks();
    const serviceName = requestNode.title || requestNode.metadata.serviceName || state.execution.serviceName || "orders-api";
    state.execution.serviceName = serviceName;
    requestNode.metadata.serviceName = serviceName;

    return {
      manifest: {
        sigla: requestNode.metadata.sigla || "",
        sigla_app: requestNode.metadata.siglaApp || "",
        service_name: serviceName,
        server: {
          id: requestNode.metadata.requestName || "httpServer",
          kind: requestNode.metadata.serverKind || "HTTP",
          route_method: requestNode.metadata.routeMethod || "POST",
          route_path: requestNode.metadata.routePath || "/",
          port: Number(requestNode.metadata.port) || 8080
        },
        execution_steps: steps,
        resources: summarizeResources(),
        response: aggregatorNode ? {
          id: aggregatorNode.metadata.aggregationName || sanitizeStepId(aggregatorNode.title),
          status: Number(aggregatorNode.metadata.responseStatus) || 200,
          state_key: aggregatorNode.metadata.stateKey || "response",
          expression: aggregatorNode.metadata.expression || ""
        } : {
          id: terminalLabel,
          status: 200,
          state_key: "response",
          expression: "output.OK(\"response\")"
        },
        example_payload: payload,
        examples: mocks
      },
      stepNodeMap,
      terminalNodeMap
    };
  }

  function compileNodeRecursive(node, context) {
    if (!node || context.pathVisited.has(node.id)) {
      throw new Error(`Loop detectado proximo de ${node ? node.title : "desconhecido"}.`);
    }

    const nextVisited = new Set(context.pathVisited);
    nextVisited.add(node.id);

    if (isOutputNode(node)) {
      context.terminalNodeMap[context.terminalLabel] = node.id;
      return {
        entryTarget: context.terminalLabel,
        steps: [],
        openTerminals: []
      };
    }

    if (isIntegrationNode(node)) {
      const compileBranch = (portKey, suffix) => {
        const edge = state.edges.find((item) => item.sourceNodeId === node.id && item.sourcePort === portKey);
        const targetNode = edge ? getNodeById(edge.targetNodeId) : null;
        if (!targetNode) {
          return { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: false };
        }
        if (isOutputNode(targetNode)) {
          context.terminalNodeMap[context.terminalLabel] = targetNode.id;
          return { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: true };
        }
        const result = compileNodeRecursive(targetNode, {
          ...context,
          suffix,
          pathVisited: nextVisited
        });
        return { ...result, hasVisualTarget: true };
      };

      const stepId = getNodeBusinessId(node);
      context.stepNodeMap[stepId] = node.id;
      const emptyBranch = { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: false };
      const successResult = (node.metadata.successAction || "next_step") === "next_step"
        ? compileBranch("success", `${context.suffix}-success`)
        : emptyBranch;
      const failureResult = (node.metadata.failureAction || "return_error") === "next_step"
        ? compileBranch("failure", `${context.suffix}-failure`)
        : emptyBranch;

      const step = {
        step_id: stepId,
        type: "integration",
        resource_type: node.metadata.resourceType || "restapi",
        source: node.metadata.source || node.metadata.resourceName || sanitizeStepId(node.title),
        operation: node.metadata.operation || node.metadata.method || "Call",
        endpoint: node.metadata.endpoint || "",
        lookup_key: node.metadata.lookupKey || "payload.id",
        on_success: compileConditionalOutcome(node, "success", successResult, context),
        on_failure: compileConditionalOutcome(node, "failure", failureResult, context)
      };

      return {
        entryTarget: stepId,
        steps: [step, ...successResult.steps, ...failureResult.steps],
        openTerminals: [
          ...successResult.openTerminals,
          ...failureResult.openTerminals
        ]
      };
    }

    if (isDecisionNode(node)) {
      const compileBranch = (portKey, suffix) => {
        const edge = state.edges.find((item) => item.sourceNodeId === node.id && item.sourcePort === portKey);
        const targetNode = edge ? getNodeById(edge.targetNodeId) : null;
        if (!targetNode) {
          return { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: false };
        }
        if (isOutputNode(targetNode)) {
          context.terminalNodeMap[context.terminalLabel] = targetNode.id;
          return { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: true };
        }
        const result = compileNodeRecursive(targetNode, {
          ...context,
          suffix,
          pathVisited: nextVisited
        });
        return { ...result, hasVisualTarget: true };
      };

      const emptyBranch = { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: false };
      const successResult = (node.metadata.successAction || "next_step") === "next_step"
        ? compileBranch("success", `${context.suffix}-success`)
        : emptyBranch;
      const failureResult = (node.metadata.failureAction || "return_error") === "next_step"
        ? compileBranch("failure", `${context.suffix}-failure`)
        : emptyBranch;
      const stepId = getNodeBusinessId(node);
      context.stepNodeMap[stepId] = node.id;
      const step = {
        step_id: stepId,
        type: "decision",
        rule_name: node.metadata.ruleName || stepId,
        cel_expression: getConditionalExpression(node),
        on_success: compileConditionalOutcome(node, "success", successResult, context),
        on_failure: compileConditionalOutcome(node, "failure", failureResult, context)
      };

      const openTerminals = [
        ...successResult.openTerminals,
        ...failureResult.openTerminals
      ];

      return {
        entryTarget: step.step_id,
        steps: [step, ...successResult.steps, ...failureResult.steps],
        openTerminals
      };
    }

    const edge = state.edges.find((item) => item.sourceNodeId === node.id && item.sourcePort === "out-main");
    const targetNode = edge ? getNodeById(edge.targetNodeId) : null;
    const nextResult = targetNode && !isOutputNode(targetNode)
      ? compileNodeRecursive(targetNode, {
        ...context,
        suffix: `${context.suffix}-next`,
        pathVisited: nextVisited
      })
      : { entryTarget: context.terminalLabel, steps: [], openTerminals: [], hasVisualTarget: Boolean(targetNode) };
    if (targetNode && isOutputNode(targetNode)) {
      context.terminalNodeMap[context.terminalLabel] = targetNode.id;
    }

    const stepId = getNodeBusinessId(node);
    context.stepNodeMap[stepId] = node.id;
    return {
      entryTarget: stepId,
      steps: [{
        step_id: stepId,
        type: getNodeRole(node),
        operation: node.metadata.operation || nodeCatalog[node.type].label,
        expression: node.metadata.expression || "",
        state_key: node.metadata.stateKey || stepId,
        on_success: {
          action: "next_step",
          target: nextResult.entryTarget || context.terminalLabel
        }
      }, ...nextResult.steps],
      openTerminals: nextResult.openTerminals
    };
  }

  function getConditionalExpression(node) {
    return node.metadata.celExpression ||
      node.conditions.find((condition) => condition.id === "success")?.expression ||
      node.conditions[0]?.expression ||
      "true";
  }

  function ensureConditionalPorts(node) {
    if (!node || !isDecisionNode(node)) return;
    const expression = node.metadata.celExpression ||
      node.conditions.find((condition) => condition.id === "success")?.expression ||
      node.conditions[0]?.expression ||
      "true";
    node.metadata.celExpression = expression;
    node.conditions = [
      { id: "success", label: "Sucesso", expression },
      { id: "failure", label: "Falha", expression: "fallback" }
    ];
  }

  function compileConditionalOutcome(node, outcome, branchResult, context) {
    const action = node.metadata[`${outcome}Action`] || (outcome === "failure" ? "return_error" : "next_step");
    if (action === "next_step") {
      const configuredTarget = node.metadata[`${outcome}Target`];
      return {
        action: "next_step",
        target: branchResult.hasVisualTarget
          ? branchResult.entryTarget || context.terminalLabel
          : configuredTarget || branchResult.entryTarget || context.terminalLabel
      };
    }

    return {
      action,
      http_status: Number(node.metadata[`${outcome}HttpStatus`]) || (action === "return_success" ? 200 : 400),
      message: node.metadata[`${outcome}Message`] || (action === "return_success" ? "Sucesso" : "Falha na condicao")
    };
  }

  function patchStepTarget(steps, stepId, target) {
    const step = steps.find((item) => item.step_id === stepId);
    if (step && step.on_success) {
      step.on_success.target = target;
    }
  }

  function getOutgoingTargets(nodeId) {
    const targets = state.edges
      .filter((edge) => edge.sourceNodeId === nodeId)
      .map((edge) => getNodeById(edge.targetNodeId))
      .filter(Boolean)
      .sort((a, b) => a.x - b.x || a.y - b.y);
    return uniqueById(targets);
  }

  function uniqueById(nodes) {
    const seen = new Set();
    return nodes.filter((node) => {
      if (seen.has(node.id)) {
        return false;
      }
      seen.add(node.id);
      return true;
    });
  }

  function summarizeResources() {
    return state.nodes
      .filter(isIntegrationNode)
      .map((node) => ({
        name: node.metadata.source || node.metadata.resourceName || sanitizeStepId(node.title),
        package: node.metadata.resourceType || "restapi",
        operation: node.metadata.operation || node.metadata.method || "Call",
        endpoint: node.metadata.endpoint || "",
        config: {
          region: node.metadata.region || "",
          profile: node.metadata.profile || "",
          endpointOverride: node.metadata.endpointOverride || "",
          table: node.metadata.table || "",
          partitionKey: node.metadata.partitionKey || "",
          sortKey: node.metadata.sortKey || "",
          driver: node.metadata.driver || "",
          dsn: node.metadata.dsn || "",
          host: node.metadata.host || "",
          port: node.metadata.port || "",
          database: node.metadata.database || "",
          username: node.metadata.username || "",
          password: node.metadata.password || "",
          sslMode: node.metadata.sslMode || "",
          maxOpenConns: node.metadata.maxOpenConns || "",
          maxIdleConns: node.metadata.maxIdleConns || "",
          queryTimeout: node.metadata.queryTimeout || "",
          addr: node.metadata.addr || "",
          db: node.metadata.db || "",
          poolSize: node.metadata.poolSize || "",
          dialTimeout: node.metadata.dialTimeout || "",
          readTimeout: node.metadata.readTimeout || "",
          writeTimeout: node.metadata.writeTimeout || "",
          keyPrefix: node.metadata.keyPrefix || "",
          bucket: node.metadata.bucket || "",
          pathStyle: node.metadata.pathStyle || "",
          pathPrefix: node.metadata.pathPrefix || "",
          secretName: node.metadata.secretName || "",
          versionStage: node.metadata.versionStage || "",
          baseURL: node.metadata.baseURL || "",
          timeout: node.metadata.timeout || "",
          defaultHeaderKey: node.metadata.defaultHeaderKey || "",
          defaultHeaderValue: node.metadata.defaultHeaderValue || "",
          auth: node.metadata.auth || "",
          authValue: node.metadata.authValue || "",
          format: node.metadata.format || "",
          skipUnknownSegments: node.metadata.skipUnknownSegments || "",
          dateLocation: node.metadata.dateLocation || "",
          segmentCode: node.metadata.segmentCode || "",
          layoutFields: node.metadata.layoutFields || ""
        }
      }));
  }

  function getLeftMostNode() {
    return state.nodes.slice().sort((a, b) => a.x - b.x || a.y - b.y)[0] || null;
  }

  function generateGoSnippet(plan) {
    const serviceName = sanitizeStepId(plan.service_name || "app");
    const server = plan.server || {};
    const routeMethod = String(server.route_method || "POST").toUpperCase();
    const routePath = server.route_path || "/";
    const port = server.port || 8080;
    const response = plan.response || {};
    const responseStatus = Number(response.status) || 200;
    const responseStateKey = response.state_key || "response";
    const statusExpr = responseStatus === 201
      ? "http.StatusCreated"
      : responseStatus === 200
        ? "http.StatusOK"
        : responseStatus === 202
          ? "http.StatusAccepted"
          : responseStatus === 204
            ? "http.StatusNoContent"
            : responseStatus === 400
              ? "http.StatusBadRequest"
              : responseStatus === 403
                ? "http.StatusForbidden"
                : responseStatus === 404
                  ? "http.StatusNotFound"
                  : responseStatus === 500
                    ? "http.StatusInternalServerError"
                    : String(responseStatus);
    const resources = Array.isArray(plan.resources) ? plan.resources : [];
    const steps = Array.isArray(plan.execution_steps) ? plan.execution_steps : [];

    const imports = new Set([
      "\"context\"",
      "\"net/http\"",
      "\"github.com/raywall/go-code-blocks/blocks/flow\"",
      "\"github.com/raywall/go-code-blocks/blocks/output\"",
      "\"github.com/raywall/go-code-blocks/blocks/server\"",
      "\"github.com/raywall/go-code-blocks/core\""
    ]);

    resources.forEach((resource) => {
      imports.add(`"github.com/raywall/go-code-blocks/blocks/${resource.package || "restapi"}"`);
    });
    if (steps.some((step) => step.type === "decision")) {
      imports.add("\"github.com/raywall/go-code-blocks/blocks/decision\"");
    }

    const resourceLines = resources.map((resource) => {
      const pkg = resource.package || "restapi";
      const name = sanitizeStepId(resource.name || pkg);
      const config = resource.config || {};
      if (pkg === "dynamodb") {
        return `    ${name} := dynamodb.New[map[string]any]("${name}", dynamodb.WithRegion("${config.region || "us-east-1"}"), dynamodb.WithTable("${config.table || resource.endpoint || name}"), dynamodb.WithPartitionKey("${config.partitionKey || "id"}"))`;
      }
      if (pkg === "restapi") {
        return `    ${name} := restapi.New("${name}", restapi.WithBaseURL("${config.baseURL || resource.endpoint || "https://api.example.com"}"))`;
      }
      if (pkg === "redis") {
        return `    ${name} := redis.New("${name}", redis.WithAddr("${config.addr || resource.endpoint || "localhost:6379"}"), redis.WithKeyPrefix("${config.keyPrefix || ""}"))`;
      }
      if (pkg === "s3") {
        return `    ${name} := s3.New("${name}", s3.WithRegion("${config.region || "us-east-1"}"), s3.WithBucket("${config.bucket || resource.endpoint || name}"))`;
      }
      if (pkg === "parameterstore") {
        return `    ${name} := parameterstore.New("${name}", parameterstore.WithRegion("${config.region || "us-east-1"}"), parameterstore.WithPathPrefix("${config.pathPrefix || resource.endpoint || "/myapp/dev"}"), parameterstore.WithDecryption())`;
      }
      if (pkg === "secretsmanager") {
        return `    ${name} := secretsmanager.New("${name}", secretsmanager.WithRegion("${config.region || "us-east-1"}"))`;
      }
      if (pkg === "rds") {
        return `    ${name} := rds.New("${name}", rds.WithDriver(rds.DriverPostgres), rds.WithDSN("${config.dsn || resource.endpoint || "postgres://user:pass@localhost:5432/app?sslmode=disable"}"))`;
      }
      if (pkg === "cnab") {
        return `    ${name} := cnab.New("${name}", cnab.WithFormat(cnab.Format240))`;
      }
      return `    ${name} := ${pkg}.New("${name}")`;
    });

    const flowLines = steps.map((step) => {
      const id = sanitizeStepId(step.step_id || "step");
      if (step.type === "decision") {
        return `        flow.NewStep("${id}", flow.Validate(rules, "${step.rule_name || id}", func(req *server.Request, _ *flow.State) map[string]any { return map[string]any{} })),`;
      }
      if (step.type === "integration") {
        return `        flow.EnrichStep("${id}", func(ctx context.Context, req *server.Request, s *flow.State) (any, error) { return nil, nil }), // ${step.resource_type}.${step.operation}`;
      }
      return `        flow.NewStep("${id}", flow.Transform(func(ctx context.Context, req *server.Request, s *flow.State) error { return nil })),`;
    });

    const routeCall = routeMethod === "GET" || routeMethod === "POST" || routeMethod === "PUT" || routeMethod === "PATCH" || routeMethod === "DELETE"
      ? `router.${routeMethod}("${routePath}", appFlow.Handler())`
      : `router.Handle("${routeMethod}", "${routePath}", appFlow.Handler())`;

    const registerLines = [
      ...resources.map((resource) => `    app.MustRegister(${sanitizeStepId(resource.name || resource.package || "resource")})`),
      steps.some((step) => step.type === "decision") ? "    app.MustRegister(rules)" : "",
      "    app.MustRegister(appFlow)",
      "    app.MustRegister(api)"
    ].filter(Boolean);

    return `package main

import (
    ${[...imports].sort().join("\n    ")}
)

func main() {
    ctx := context.Background()

${resourceLines.length ? resourceLines.join("\n") : "    // Declare blocos de integracao aqui."}
${steps.some((step) => step.type === "decision") ? "    rules := decision.New(\"rules\")\n" : ""}
    appFlow := flow.New("${serviceName}",
${flowLines.length ? flowLines.join("\n") : "        flow.NewStep(\"respond\", output.OK(\"response\")),"}
        flow.NewStep("respond", output.JSON(${statusExpr}, "${responseStateKey}")),
    )

    router := server.NewRouter()
    ${routeCall}

    api := server.NewHTTP("${server.id || "httpServer"}",
        server.WithPort(${port}),
        server.WithRouter(router),
        server.WithMiddleware(server.Logging(), server.Recovery()),
    )

    app := core.NewContainer()
${registerLines.join("\n")}
    app.InitAll(ctx)
    defer app.ShutdownAll(ctx)
    api.Wait()
}`;
  }

  function wait(ms) {
    return new Promise((resolve) => window.setTimeout(resolve, ms));
  }

  function getNodeById(nodeId) {
    return state.nodes.find((node) => node.id === nodeId) || null;
  }

  function getNodesBounds(nodes) {
    const xs = nodes.map((node) => node.x);
    const ys = nodes.map((node) => node.y);
    const maxXs = nodes.map((node) => node.x + node.width);
    const maxYs = nodes.map((node) => node.y + node.height);
    const minX = Math.min(...xs);
    const minY = Math.min(...ys);
    const maxX = Math.max(...maxXs);
    const maxY = Math.max(...maxYs);
    return {
      minX,
      minY,
      maxX,
      maxY,
      width: maxX - minX,
      height: maxY - minY
    };
  }

  function createId(prefix) {
    return `${prefix}-${Math.random().toString(36).slice(2, 9)}`;
  }

  function sanitizeStepId(value) {
    return String(value || "step")
      .replace(/[^a-zA-Z0-9_]+/g, "_")
      .replace(/^_+|_+$/g, "") || "step";
  }

  function titleFromRequestId(value) {
    return String(value || "request").replaceAll("_", " ");
  }

  function getNodeBusinessId(node) {
    if (!node) return "step";
    if (isEntryNode(node)) {
      return String(node.metadata.requestName || "request");
    }
    if (isOutputNode(node)) {
      return String(node.metadata.aggregationName || sanitizeStepId(node.title));
    }
    return String(node.metadata.stepId || sanitizeStepId(node.title));
  }

  function setNodeBusinessId(node, value) {
    const nextId = String(value || "").trim();
    const previousId = getNodeBusinessId(node);
    const previousTitle = titleFromRequestId(previousId);

    if (isOutputNode(node)) {
      node.metadata.aggregationName = nextId;
    } else {
      node.metadata.stepId = nextId;
    }

    if (!node.title || node.title === previousTitle) {
      node.title = titleFromRequestId(nextId);
    }
  }

  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  function isPlainObject(value) {
    return Boolean(value) && typeof value === "object" && !Array.isArray(value);
  }

  function formatJsonForEditor(value) {
    return JSON.stringify(value ?? {}, null, 2);
  }

  function parseJsonEditor(text, label) {
    try {
      return JSON.parse(text || "{}");
    } catch (error) {
      throw new Error(`${label}: ${error.message}`);
    }
  }

  function hasJsonContent(text) {
    if (!String(text || "").trim()) {
      return false;
    }
    try {
      const parsed = JSON.parse(text);
      if (Array.isArray(parsed)) {
        return parsed.length > 0;
      }
      if (isPlainObject(parsed)) {
        return Object.keys(parsed).length > 0;
      }
      return parsed !== null && parsed !== "";
    } catch (error) {
      return true;
    }
  }

  function resolveRequestPayloadText(node) {
    return node.metadata.payloadText ||
      state.execution.payloadText ||
      formatJsonForEditor(defaultExecutionPayload());
  }

  function resolveFetchMockText(node) {
    if (hasJsonContent(node.metadata.mockText)) {
      return node.metadata.mockText;
    }

    return formatJsonForEditor(resolveFetchMock(node));
  }

  function resolveFetchMock(node) {
    try {
      const mocks = parseJsonEditor(state.execution.mocksText || "{}", "Mocks");
      const source = node.metadata.source || node.metadata.resourceName || sanitizeStepId(node.title);
      return mocks[source] || {};
    } catch (error) {
      return {};
    }
  }

  function hydrateNodeTestDataFromExecution() {
    const requestNode = state.nodes.find(isEntryNode);
    if (requestNode && !hasJsonContent(requestNode.metadata.payloadText)) {
      requestNode.metadata.payloadText = state.execution.payloadText || formatJsonForEditor(defaultExecutionPayload());
    }

    state.nodes
      .filter((node) => isIntegrationNode(node) && !hasJsonContent(node.metadata.mockText))
      .forEach((node) => {
        const mock = resolveFetchMock(node);
        if (hasJsonContent(formatJsonForEditor(mock))) {
          node.metadata.mockText = formatJsonForEditor(mock);
        }
      });
  }

  function buildExecutionPayload() {
    const requestNode = state.nodes.find(isEntryNode);
    const payloadText = requestNode
      ? resolveRequestPayloadText(requestNode)
      : state.execution.payloadText || formatJsonForEditor(defaultExecutionPayload());
    const payload = parseJsonEditor(payloadText, "Payload do request");
    state.execution.payloadText = formatJsonForEditor(payload);
    if (requestNode) {
      requestNode.metadata.payloadText = state.execution.payloadText;
    }
    return payload;
  }

  function buildExecutionMocks() {
    const mocks = {};
    state.nodes
      .filter(isIntegrationNode)
      .forEach((node) => {
        const source = node.metadata.source || node.metadata.resourceName || sanitizeStepId(node.title);
        mocks[source] = parseJsonEditor(resolveFetchMockText(node), `Mock de ${source}`);
      });

    if (!Object.keys(mocks).length) {
      return defaultExecutionMocks();
    }

    state.execution.mocksText = formatJsonForEditor(mocks);
    return mocks;
  }

  function defaultExecutionMocks() {
    return {
      customersDB: {
        "CUST-1": { id: "CUST-1", customer_type: "PJ", name: "Acme Ltda" }
      },
      ordersDB: {
        "ORDER-100": { id: "ORDER-100", status: "created" }
      }
    };
  }

  function defaultExecutionPayload() {
    return {
      order_id: "ORDER-100",
      customer_id: "CUST-1",
      amount: 129.9
    };
  }

  function clamp(value, min, max) {
    return Math.min(Math.max(value, min), max);
  }

  function escapeHtml(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;");
  }

  function escapeAttribute(value) {
    return escapeHtml(value).replaceAll("'", "&#39;");
  }

  function formatNodeTitleForDisplay(value) {
    return String(value || "").replaceAll("_", " ");
  }
})();
