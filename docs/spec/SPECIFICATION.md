# Codex Subscription Router para Windows — especificación de c4eb2ea

Esta es la especificación funcional y técnica de **`c4eb2eae4fc5c59987a901d373f7b58f5015c3a9`**, incorporado el 28 de septiembre de 2026 mediante el PR #9: soporte para Codex 26.924. Describe lo heredado del proyecto original y lo añadido o modificado en el port Windows **hasta ese commit**.

Redactada el 5 de octubre de 2026 mediante lectura de objetos Git de esa revisión. No describe HEAD, cambios locales, la instalación actual ni funciones posteriores. Es documentación nueva sobre una versión histórica: este documento y su visor no existían dentro de `c4eb2ea`.

## Resumen

### Referencia única y propósito

Estado: Histórico

| Campo | Referencia |
| --- | --- |
| Repositorio | TheDaniXSX/codex-subscription-router-windows |
| Commit de la especificación | `c4eb2eae4fc5c59987a901d373f7b58f5015c3a9` |
| Árbol Git | `1a2869e94301b18c0b4848e5bd8e15faae8d820d` |
| Fecha del commit | 28 de septiembre de 2026, 10:49:02 +02:00 |
| Archivos versionados | 267 |
| Versión declarada del proyecto | 0.2.0, preview distribuida como código fuente |
| Perfil Codex incorporado por el commit | Windows `26.924.2738.0`, ASAR `26.924.22138`, build `11645` |

La referencia permite reconstruir qué hacía el router antes de las ampliaciones de analítica y PROD/DEV. **No demuestra que fuera la última versión sin fallos.** Para declararla estable se necesitarían pruebas sobre la aplicación y los binarios exactos; este trabajo redacta la especificación, no realiza esa recuperación.

El objetivo funcional es utilizar varias suscripciones ChatGPT desde una copia independiente de Codex. La cuenta que guarda una conversación puede ser distinta de la cuenta usada por su próxima inferencia. Auto decide por petición; el modo manual impone una suscripción concreta sin deshabilitar las demás.

Fuentes: [commit fijado](https://github.com/TheDaniXSX/codex-subscription-router-windows/commit/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9), [README de esa revisión](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/README.md), [identidad documental local](source-evidence.json).

### Cómo leer los requisitos

Cada identificador representa un contrato que debe conservarse al recuperar esta base. Las explicaciones describen la implementación encontrada, incluidas sus condiciones y límites; no convierten una carencia en una función deseada.

| Etiqueta | Significado en este documento |
| --- | --- |
| Implementado | Existe código para ese comportamiento en `c4eb2ea`. No implica que esté activo ahora. |
| Cubierto | Se ha localizado una prueba pertinente en ese commit. No implica ejecución durante esta redacción. |
| Histórico | La afirmación pertenece a esa revisión o a un informe guardado en ella. |
| Pendiente | Falta aceptación en vivo, hay una limitación o se requiere una decisión posterior. |

Las identidades no son intercambiables: **suscripción** es cuenta y cuota; **modelo** es el modelo solicitado; **propietario del historial** decide dónde se conserva y consulta el hilo; **suscripción de gasto** decide qué credenciales se usan para una petición; **cuenta de Apps/MCP** y **cuenta del modal de resets** son selecciones independientes.

### Procedencia de las funciones

Estado: Histórico · Implementado

El proyecto no rehace la interfaz de Codex. Copia la aplicación oficial instalada y modifica determinados bundles y puntos de arranque. La interfaz, Electron, CLI y helpers nativos siguen procediendo de OpenAI; el router añade un multiplexor, un transporte de gasto y controles integrados.

| Procedencia | Funcionalidades presentes en esta base |
| --- | --- |
| Proyecto original de b-nnett | Multiplexor Go; app-server por cuenta; registro de cuentas y propietario del hilo; gestión multicuenta; login por código; cuotas agrupadas; perfil combinado/individual; Apps/MCP por cuenta; resets scoped; selección por cuota y reset; comportamiento sticky/failover legado. |
| Port y ampliaciones Windows | Descubrimiento del paquete Store; patcher con versiones y hashes exactos; launcher, homes/perfil/cachés/logs propios; ACL y endurecimiento del control; aislamiento de registros oficiales; ciclo de instalación/backup/rollback; icono y AppUserModelID; integración shell propia y Chrome independiente opt-in. |
| Cambios funcionales propios ya incorporados | Selector Auto/suscripción estricta; gasto por cada inferencia y continuación, también en subagentes nativos; separación historial/gasto; atribución persistente de la última petición aceptada por hilo; correcciones de Rename, Usage/reset y navegación; gateway compatible con preparación de voz. |
| Adaptación 26.924 | Nuevos anchors y chunks; helpers del menú disponibles al navegar a perfil/plugins; refresco y wrappers React corregidos; bootstrap/updater; preflight de rutas; parseo de todos los bundles modificados; comprobación del runtime CUA con versión Node sufijada; digest ASAR de Electron. |

La licencia del código del proyecto es MIT y conserva los avisos del autor original. El activo del logo incorpora sus avisos de procedencia/licencia. La aplicación y los binarios de OpenAI no quedan cubiertos por esa licencia y no se redistribuyen.

Fuentes: [NOTICE](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/NOTICE.md), [paridad Windows](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/WINDOWS-PARITY.md), [cualificación 26.924](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/UPGRADE-26924-PLAN.md).

### Qué queda expresamente fuera

Esta especificación **no incorpora** estimaciones de coste API, contabilización de tokens por turno, calibración del porcentaje de cuota, catálogo de precios, ledger SQLite propio, histórico analítico persistente, exclusión de muestras de calibración, despliegue PROD/DEV, broker entre dos escritorios, leases de ejecución, sincronización CAS de estado Electron ni protección de borradores de esa sincronización.

La consulta de **usage remaining**, las cuotas por cuenta, la puntuación Auto y los resets **sí existían**. No son los sistemas analíticos de gasto y costes añadidos después. Tampoco se elimina el aislamiento original de cuentas por el hecho de excluir la separación posterior de entornos PROD/DEV.

## Funcionalidades

### Cuentas y sesiones

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| CNT-001 | Mantener una cuenta Primary ligada al `CODEX_HOME` heredado; si no existe, usar `%USERPROFILE%\.codex`. No duplicar su autenticación para crear la cuenta principal. |
| CNT-002 | Crear para cada suscripción secundaria un identificador estable y un `codex-home` aislado, con credenciales, datos y almacenamiento CLI/MCP propios. |
| CNT-003 | Añadir suscripciones mediante el flujo oficial de código de dispositivo; mostrar URL/código, permitir copiar/abrir y cancelar, y reflejar el estado del login. |
| CNT-004 | Mostrar conexión, cuota e identidad por cuenta; permitir renombrar sin cambiar su ID, propietario de hilos o selección de gasto. |
| CNT-005 | Permitir habilitar/deshabilitar secundarias. Una deshabilitada no participa en Auto ni en la cuota usable agrupada. Primary no se puede deshabilitar ni eliminar. |
| CNT-006 | Permitir logout, recuperación y retirada de cuentas mediante acciones explícitas. No eliminar o deshabilitar la suscripción seleccionada para gastar sin cambiar primero de modo. |
| CNT-007 | Mantener un único login pendiente y sus estados coherentes. Mientras el código está visible, un clic exterior no debe ocultar accidentalmente el flujo. |
| CNT-008 | Enmascarar visualmente el email en el menú hasta hover. Esto no anonimiza los datos ni impide que el usuario local vea su identidad. |

Las cuentas habilitadas disponen de un proceso oficial `app-server`. El router refresca credenciales mediante el cliente oficial; no implementa un sistema OAuth paralelo ni sobrescribe el `auth.json` de otra cuenta. La retirada de una secundaria elimina metadatos/asignaciones y su home exacto validado mediante cuarentena; no revoca la cuenta remota. Una limpieza fallida puede dejar esa cuarentena recuperable.

Rename exige un label no vacío tras trim: la UI limita a 80 caracteres y el backend a 256 bytes. Fallar el guardado conserva el borrador; fallar el refresco posterior no deshace una mutación ya confirmada. Cancelar un login conserva la cuenta para reintento. Los hijos tienen reinicio supervisado con backoff de 1–30 segundos y circuito de 60 segundos tras cinco fallos; esto no garantiza recuperación de cualquier error.

Fuentes: [gestión de cuentas](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/accounts.go), [menú](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/ui/account-menu.js), [acciones probadas](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/control/account_actions_test.go).

### Selector de gasto: Auto o suscripción concreta

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| ROU-001 | Ofrecer Auto y una opción por suscripción, incluida Primary. El selector cerrado muestra el modo guardado y se despliega al pulsarlo. Seleccionar una cuenta no deshabilita las demás. |
| ROU-002 | Aplicar el modo a cada nueva reserva de inferencia interceptada, no solo a la creación de hilos. Incluye continuaciones, hilos reanudados/fork y subagentes nativos. |
| ROU-003 | En modo explícito, gastar solo de la cuenta elegida. Si está agotada, desconectada o no dispone de cuota conocida, detener la petición sin consumir otra cuenta. |
| ROU-004 | En Auto, elegir entre cuentas habilitadas, conectadas con ChatGPT y con cuota válida, conocida y positiva. No tratar una muestra ausente o antigua como capacidad disponible. |
| ROU-005 | Serializar la reserva frente a cambios de modo y solicitudes concurrentes. Una petición ya reservada conserva su cuenta; el nuevo modo afecta a reservas posteriores. |
| ROU-006 | Persistir la selección en `routing-mode.json`, separado de `state.json`; sobrevivir a reinicios y permitir que versiones anteriores ignoren ese sidecar. |
| ROU-007 | No reintentar automáticamente una inferencia en otra suscripción. No repetir una petición cuya entrega sea incierta. |
| ROU-008 | No cambiar el modelo, nivel de razonamiento ni `service_tier` para resolver una cuenta de gasto. El router decide identidad, no transforma la petición del modelo. |

En el launcher Windows se activa `CODEX_MUX_REQUEST_SPENDING=1`. Los servidores nativos usan un provider local `codex_router_spend`, con HTTP Responses, WebSockets desactivados y reintentos de petición/stream a cero. `thread/start`, `thread/resume` y `thread/fork` se vinculan a ese provider.

La capacidad de admisión toma el mínimo de los porcentajes restantes de las ventanas corta y larga disponibles. La ventana larga es obligatoria. El conjunto de candidatos se cachea hasta dos segundos, mientras cada muestra conserva su propio momento de observación; una antigüedad superior a cinco segundos la invalida. El coste futuro en tokens no se conoce y no se reserva una cantidad inventada.

La prioridad Auto utiliza la cuota de la ventana larga y el tiempo hasta su reset:

```text
urgencia = porcentaje restante de ventana larga / horas hasta reset
           × (1 + 0,15 × min(resets acumulados, 3))
puntuación = urgencia / (1 + peticiones en vuelo de esa cuenta)
```

Sin reset futuro válido se usa la duración positiva de la ventana; si falta, siete días. El mínimo es un minuto. El bonus procede de metadatos cacheados de resets y es neutro si no están disponibles; Auto no canjea resets por sí mismo. Los desempates del modo por petición son: menos peticiones en vuelo, mayor capacidad restante y orden lexicográfico del ID. **La fecha es la del reset de cuota, no una caducidad genérica de la suscripción.**

La admisión usa las ventanas generales; los buckets específicos por modelo no están incorporados a la decisión. Un rechazo posterior del proveedor se devuelve de forma segura, no activa otro gasto. Clientes externos, tareas cloud o procesos que no pasan por el provider del router no están bajo su control. Al cargar un modo cuyo ID fue eliminado/deshabilitado por otro binario se normaliza a Auto: es recuperación del estado al cargar, no fallback de una inferencia explícita.

Fuentes: [contrato por petición](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/PER-REQUEST-SPENDING.md), [política sincronizada](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/spend/policy.go), [provider y snapshots](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/spending.go).

### Historial, herramientas y propiedad del hilo

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| HIS-001 | Persistir `thread ID → account ID` para consultar y operar el historial mediante su cuenta propietaria. No confundir esa cuenta con la seleccionada para gastar. |
| HIS-002 | En modo por petición, mantener la propiedad del historial aunque cambie la suscripción de inferencia. No migrar conversaciones para equilibrar gasto ordinario. |
| HIS-003 | Unificar el listado de conversaciones de los hijos y conservar el origen de cada hilo. Consultar hasta 2.000 hilos por cuenta en páginas de 100; el listado combinado devuelve 20 por defecto y como máximo 500 por página. |
| HIS-004 | Encauzar RPC, notificaciones y solicitudes del servidor mediante el hijo correcto; aislar los identificadores internos y conservar el intercambio de herramientas/aprobaciones. |
| HIS-005 | Preservar el comportamiento legado cuando no está habilitado el gasto por petición, sin presentarlo como el contrato normal del launcher Windows. |

La rama heredada selecciona cuenta al crear el hilo, utiliza afinidad y puede hacer failover por límite estructurado. En `c4eb2ea` sigue existiendo, condicionada por `requestSpending`. **No significa que Windows deba gastar siempre del dueño del hilo o cambiar de cuenta tras un rechazo:** su launcher activa el contrato ROU-001–008.

La lista deduplica según propietario persistido, fecha y orden estable de cuentas; comunica fallos parciales y devuelve error si todas fallan. Las aprobaciones remapean el ID de la solicitud del servidor y lo restauran al responder; no incluyen los leases ni la gestión de clientes del broker posterior.

Fuentes: [multiplexor](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/mux.go), [listado](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/thread_list.go).

### Identidad de la última petición gastada

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| ATR-001 | En Environment → Subscription mostrar la suscripción de la petición más reciente de ese hilo aceptada por el HTTP upstream, no su propietario ni el siguiente candidato Auto. |
| ATR-002 | Registrar la aceptación después de recibir cabeceras 2xx, antes de terminar el stream; ordenar por secuencia de despacho para que una petición antigua lenta no reemplace la más nueva. |
| ATR-003 | Actualizar mediante eventos y consulta autenticada, con refresco periódico y protección frente a respuestas obsoletas, navegación y cambios de hilo. Cada subagente conserva su propia atribución. |
| ATR-004 | Persistir un registro acotado por hilo en `thread-spending/<sha256-del-id>.json`. Si no existe evidencia, mostrar el estado vacío explícito; nunca adivinar Primary. |
| ATR-005 | No reemplazar la evidencia por intentos rechazados, no enviados o de entrega incierta. Una cuenta eliminada conserva ID/label observados, sin inventar su cuota actual. |

Una aceptación no es un recibo de facturación: el stream puede fallar o cancelarse después. El modal muestra la identidad upstream utilizada, **no tokens exactos ni coste monetario**. Un fallo de persistencia conserva la observación en memoria y expone `persisted: false`; no repite ni rechaza por ello la inferencia ya aceptada.

El menú dispone además de **Last inference**, alimentado por las últimas 100 observaciones de decisiones terminadas registradas en memoria. Es global y se vacía al reiniciar. No equivale a la última petición aceptada de un hilo ni representa todas las solicitudes simultáneas o todos los rechazos de preflight.

Fuentes: [contrato de atribución](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/THREAD-SPENDING.md), [estado persistente](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/state/thread_spending.go), [componente del hilo](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/ui/thread-subscription.js).

### Menú, cuotas, perfiles y resets

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| UI-001 | Integrar la gestión en el menú de perfil oficial, manteniendo la experiencia nativa; no crear un escritorio alternativo que sustituya sus funciones. |
| UI-002 | Mostrar cuota combinada y detalle por suscripción habilitada. La suma de porcentajes es capacidad agrupada, no un porcentaje normalizado de una sola cuenta. |
| UI-003 | Hacer operativos Rename, Routing mode y Usage remaining, incluso con menú cargado de forma lazy o navegación directa a perfil/plugins. |
| PERF-001 | Mostrar perfil y estadísticas combinados con avatares de cuentas; pulsar una cuenta cambia a su vista individual y pulsarla de nuevo vuelve a combinado. |
| PERF-002 | Combinar contadores disponibles sin deduplicación ficticia. “Skills explored” puede contar la misma skill en más de una cuenta porque upstream entrega contadores, no IDs de skills. |
| RST-001 | Abrir el modal nativo de límites desde Usage remaining y permitir elegir de qué cuenta se consultan cuota y resets. Esa elección es independiente del modo de gasto. |
| RST-002 | Consultar los resets disponibles y sus condiciones mediante los servicios oficiales de la cuenta elegida. |
| RST-003 | Confirmar una acción de reset sobre la cuenta capturada para esa operación, sin desviarla a Primary ni a otra cuenta por cambiar el selector. |
| RST-004 | No consumir un reset durante previews o pruebas automáticas; las pruebas usan respuestas y créditos sintéticos. La acción real requiere una decisión explícita del usuario. |

En 26.924, el menú se desplaza a un chunk lazy. El port declara las dependencias necesarias para perfil/plugins, conserva los wrappers React, utiliza el callback de refresco correcto y espera a que estén listos los helpers antes de suscribir el componente del hilo a eventos. Un test del HTML/renderer no demuestra por sí solo que cada clic funcione en una sesión real.

El modal se adapta para lectura y consumo scoped de resets, pero esta base no incluye el guard posterior de checkout para secundarias ni su neutralización específica de actualizaciones optimistas de cuota Primary. No se extiende el contrato de resets a compras o mutaciones nativas no revisadas.

Fuentes: [perfil combinado](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/combined_profile.go), [resets](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/rate_limit_resets.go), [renderer 26.924](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/scripts/windows_renderer_26924.py), [corrección del menú](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/MENU-ACTIONS-FIX.md).

### Apps, plugins y MCP

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| PLG-001 | Ofrecer un selector de suscripción en Settings → Plugins para gestionar Apps/conexiones y operaciones MCP con la identidad elegida. |
| PLG-002 | Compartir definiciones de plugins y configuración MCP administrada derivada de Primary; mantener sesiones OAuth y almacenamiento de autenticación scoped por cuenta. |
| PLG-003 | Usar marcadores internos para las operaciones scoped y retirarlos antes de enviar una solicitud válida al app-server oficial estricto. |

La configuración compartida puede incluir secretos inline que se copian a los homes secundarios. Los homes separan sesiones, pero no constituyen fronteras absolutas de secretos. El scope cubre las operaciones listadas en el código: Apps list/installed/read, estado MCP y login OAuth MCP; no cualquier integración. Si el marcador identifica una cuenta inexistente/deshabilitada, la rama encontrada cae a la ruta habitual sin retirarlo: es una limitación estática, no un fallo reproducido en vivo.

Fuente: [solicitudes scoped](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/mux/scoped_request.go), [configuración](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/state/config.go).

### Transporte de inferencias y modo voz

Estado: Implementado · Cubierto · Histórico · Pendiente

| ID | Contrato |
| --- | --- |
| TRA-001 | Interceptar Responses y compaction mediante un gateway HTTP autenticado que escucha solo en loopback; seleccionar credenciales por reserva y refrescarlas con el cliente oficial. |
| TRA-002 | Conservar el cuerpo completo y el razonamiento opaco cifrado; no alterar el contenido para cruzar cuentas. No seguir redirects; en inferencia/voz sustituir las credenciales/cookies del llamante por la identidad seleccionada. |
| TRA-003 | Aceptar hasta 128 MiB de JSON sin compresión, incluidas imágenes base64 del historial; rechazar antes de elegir cuenta un cuerpo excesivo, inválido o de tipo no admitido. |
| TRA-004 | Rechazar continuaciones server-side mediante `previous_response_id`/`conversation`, modo background y peticiones comprimidas no cualificadas. No habilitar WebSockets o reintentos para ocultar una incompatibilidad. |
| VOI-001 | Admitir el establecimiento de llamada de voz compatible con los endpoints, queries y formato multipart revisados; conservar la negociación SDP y los encabezados permitidos sin exponer el token del gateway. |
| VOI-002 | Elegir la suscripción durante la creación de la llamada. Un cambio de modo posterior no migra una llamada WebRTC ya establecida ni su audio. |
| VOI-003 | Registrar atribución de voz solo cuando existe un `Thread-Id` explícito válido. No usar un ID de sesión o del padre como si fuera el hilo de esa petición. |

El descubrimiento read-only `/v1/models` no reserva inferencia y conserva las cabeceras de autenticación/cuenta del llamante: no se describe como una operación gastada de la cuenta seleccionada. Acepta una query `client_version` acotada, no una URL arbitraria.

La voz acepta `/v1/live` y `/v1/realtime/calls` con multipart limitado exactamente a `sdp` y `session`, sin duplicados/archivos; session es JSON objeto y SDP debe comenzar con `v=0`. Traduce a JSON hacia el endpoint fijo de backend con `intent=quicksilver&architecture=avas`. Una llamada creada produce `voice-call-created`, no un resultado que asegure conversación completada.

El soporte de transporte/negociación no equivale a verificar micrófono, audio bidireccional, permisos, herramientas de voz o reconexión del escritorio. La aceptación de una SDP no demuestra una llamada funcional completa. Tampoco convierte cada paquete de audio en una nueva reserva de gasto.

Fuentes: [gateway](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/spend/gateway.go), [gateway de voz](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/spend/voice.go), [compatibilidad de voz](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/VOICE-COMPATIBILITY.md).

### Computer Use y Appshots

Estado: Implementado · Cubierto · Histórico · Pendiente

| ID | Contrato |
| --- | --- |
| NAT-001 | Preservar los helpers y el payload CUA de la fuente oficial; comprobar layout, hashes, procedencia y concordancia de versiones. No reemplazarlos por una implementación propia. |
| NAT-002 | Mantener los puentes/configuración que necesita el runtime y comprobarlos por perfil exacto; no afirmar paridad porque existan los archivos. |
| NAT-003 | Mantener Appshots desactivado por defecto; habilitar su puente solo con `CODEX_ROUTER_ENABLE_APPSHOTS=1` literal, bridge disponible y elegibilidad upstream conservada. |
| NAT-004 | Evitar un segundo mux en el app-server auxiliar CUA solo cuando el padre coincide con el helper bundled `@oai/sky/.../codex-computer-use.exe` y el comando tiene la forma admitida. No basta un nombre de proceso o flag externo. |

La comprobación estática no demuestra captura, interacción con apps, permisos, aislamiento del proceso o el ciclo completo de Computer Use. El inspector verifica el paquete `@oai/cua` y el detector auxiliar reconoce el padre `@oai/sky`; no se deduce una incompatibilidad sin comprobar el recorrido real de esa build. El recurso PE derivado de Electron pierde la validez de su firma original al cambiar el digest del ASAR; no hay en esta revisión una prueba que permita atribuir cualquier fallo de CUA exclusivamente a esa modificación.

Fuente: [cualificación de capacidades](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/WINDOWS-CAPABILITY-QUALIFICATION.md), [verificador estático](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/scripts/qualify_windows_capabilities.py).

### Chrome e integración con Windows

Estado: Implementado · Cubierto · Histórico · Pendiente

| ID | Contrato |
| --- | --- |
| BRW-001 | Neutralizar las ramas heredadas de registro/desregistro Chrome en la copia del ASAR; no modificar el host, manifest o clave `com.openai.codexextension` de la app oficial. |
| BRW-002 | Ofrecer aparte una extensión MV3 y native host propios, opt-in, con nombre/origen/registro independientes y envío explícito del contexto de la pestaña. |
| BRW-003 | Validar otra vez el origen en el native host y autenticar el salto loopback. La extensión no recibe el token de control. |
| BRW-004 | Leer estado y puerto desde los manifests schema 2 de esta base; instalar/desinstalar mediante receipts y comparación de propiedad, no sobrescribir claves ajenas. |
| BRW-005 | No declarar integración completa hasta verificar el consumo del contexto por el escritorio y el E2E de la extensión/host exactos. No atribuirle captura de pantalla ni navegación DOM completa. |
| WIN-001 | Crear el acceso directo y launcher propios, con el logo incluido y AppUserModelID independiente `com.openai.codex.subscription-router`. |
| WIN-002 | Usar la misma identidad de launcher y ventana para la agrupación en la barra de tareas; no reclamar el AppID ni publisher oficiales. |
| WIN-003 | Registrar opcionalmente el protocolo propio `codex-router://` y verbos clásicos Explorer bajo HKCU. No apropiarse de `codex://`, COM o los verbos de OpenAI. |
| WIN-004 | Validar rutas/argumentos de activación y retirar solo integraciones cuya propiedad siga correspondiendo al router. Mantener la instalación oficial intacta. |

El launcher tiene recursos/icono propios y aplica el icono a las ventanas del proceso real. Eso no significa que todos los recursos PE de cada proceso auxiliar o cada vista del gestor de tareas hayan sido sustituidos por el mismo icono. Las integraciones opt-in no forman parte de un instalador público distribuido.

Fuentes: [Chrome](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/CHROME-CONNECTOR-RELEASE.md), [host](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/chromenative/bridge.go), [shell](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/WINDOWS-SHELL-INTEGRATION.md), [icono de ventana](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/cmd/windows-launcher/window_icon_windows.go).

## Arquitectura

### Componentes y flujo

Estado: Implementado · Histórico

```text
Paquete oficial de Codex, solo lectura
        │ copia + parcheo exacto en staging
        ▼
ChatGPT.exe — launcher Windows propio
        │ perfil, estado, cachés y logs independientes
        ▼
ChatGPT.real.exe — escritorio oficial derivado
        │ una conexión app-server por stdio
        ▼
resources/codex.exe — multiplexor Go
        ├── API de control autenticada / SSE
        ├── cuenta Primary → codex.real.exe app-server
        ├── cuenta secundaria → codex.real.exe app-server
        └── gateway de inferencias
                  │ cuenta elegida por nueva reserva
                  ▼
             servicio upstream

Propietario del hilo ── historial y RPC de esa cuenta
Suscripción elegida ─── credenciales de la inferencia
```

No hay broker compartido PROD/DEV en esta base. El wrapper ejecuta el multiplexor para el modo `app-server` interactivo; para otros comandos hace passthrough al CLI oficial, conservando stdin/stdout/stderr y código de salida.

| Componente | Responsabilidad y ubicación en el commit |
| --- | --- |
| Launcher | Perfil exclusivo, resolución segura de binarios y raíces, argumentos/códigos de salida, job de procesos, identidad/icono. `cmd/windows-launcher`. |
| Entrypoint mux | Selección app-server/passthrough, contexto Windows y cierre. `cmd/codex-mux`. |
| Backend | Procesos oficiales, mensajes JSONL, IDs internos, cierre y control del árbol de procesos. `internal/backend`, `internal/protocol`. |
| Mux | Cuentas, historial, perfiles, Apps/MCP, resets, selección y observaciones. `internal/mux`. |
| Política/gateway | Reserva sincronizada, credenciales por cuenta, HTTP/SSE, compaction y voz. `internal/spend`. |
| Estado | JSON privado/atómico, routing-mode y sidecars de atribución. `internal/state`, `internal/securefs`. |
| Control | API local token-auth y SSE para renderer. `internal/control`. |
| Interfaz | Menú y atribución de hilo, inyecciones por perfil y harness de bundles reales. `ui`, `scripts/windows_renderer_26924.py`. |
| Build/lifecycle | Inventario, parcheo, hashes, ACL, verificación, instalación y rollback. `scripts`, `scripts/windows`. |
| Chrome opcional | MV3, framing Native Messaging y bridge control local. `chrome-extension`, `internal/chromenative`, `cmd/chrome-native-host`. |
| Distribución | Source preview; herramientas portable y MSIX experimental independientes. `scripts/release`, `packaging/windows`. |

Fuente: [arquitectura histórica](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/WINDOWS-ARCHITECTURE.md). Sus apartados sticky/failover se interpretan como rama legado, no como gasto por petición del launcher Windows.

### Directorios y datos persistentes

Estado: Implementado · Cubierto · Histórico

| Ubicación | Contenido y conservación |
| --- | --- |
| `InstallLocation` de OpenAI.Codex | Fuente oficial protegida, solo lectura. Nunca se modifica su instalación ni ACL. |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router` | Copia de programa reemplazable. Ejecutable del launcher, escritorio derivado, ASAR y CLI/mux. |
| `%LOCALAPPDATA%\Programs\Codex Subscription Router Data` | Raíz de datos persistente fuera del programa. Sobrevive a una actualización del programa. |
| `<data-root>\state.json` | Metadatos de cuentas y propietario de cada hilo; no guarda los tokens OAuth. |
| `<data-root>\routing-mode.json` | Auto o ID de la suscripción explícita. |
| `<data-root>\thread-spending` | Última aceptación por hilo, mediante archivos privados acotados. No contiene prompts ni contenido de respuesta. |
| `<data-root>\control-token` | Secreto de la API de control, 32 bytes aleatorios representados en hexadecimal. |
| `<data-root>\accounts\<id>\codex-home` | Credenciales, configuración y datos de cada secundaria. |
| `CODEX_HOME` o `%USERPROFILE%\.codex` | Home existente de Primary. No es independiente de la cuenta primaria oficial por diseño. |
| `<data-root>\Profile` | Perfil Electron propio y ámbito de instancia única independiente. |
| `<data-root>\runtime-cache`, `<data-root>\logs` | Cachés y logs desviados de las rutas hardcodeadas oficiales. |
| Backup hermano del programa | Árbol anterior recuperable; no equivale a una copia íntegra de todos los homes/historiales. |

El launcher resuelve raíz y puerto conjuntamente desde `resources/codex-router/launcher-config.json`, sin permitir que overrides de entorno los desvíen. El sidecar schema 2 contiene exactamente `schemaVersion`, `stateRoot` absoluto y `controlPort`; rechaza campos desconocidos. Schema 1 solo es legible para diagnóstico/migración y no permite un lanzamiento normal. El puerto se elige libre mediante CSPRNG dentro de 49152–65535 y debe coincidir con manifest y renderer.

Se rechaza un `--user-data-dir` externo que pudiera escapar del aislamiento. `--router-self-test` comprueba rutas y binarios sin iniciar la aplicación. El launcher no sustituye la verificación de todos los hashes del payload: esa tarea corresponde al verifier/lifecycle.

No se cambia `LOCALAPPDATA` globalmente. Se parchean específicamente las rutas de perfil, caché y logging. Los datos no se colocan dentro del programa para evitar que su reemplazo los destruya.

### Seguridad y fronteras reales

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| SEC-001 | Enlazar control y gateway solo a `127.0.0.1`; exigir secretos adecuados en sus rutas privadas. El token de control nunca se entrega en la URL o a la extensión Chrome. |
| SEC-002 | Limitar el origen de renderer permitido; no habilitar un servicio accesible desde la LAN ni un proxy genérico hacia URLs arbitrarias. |
| SEC-003 | Devolver metadatos/cuotas/resultados scoped, nunca tokens OAuth de cuentas mediante la API de control. |
| SEC-004 | Proteger la raíz de datos con ACL sin herencia: usuario actual y SYSTEM; releer/verificar la ACL y abortar si no es correcta. |
| SEC-005 | Escribir el estado mediante mecanismos privados y atómicos; validar schema, campos, identidad y límites; rechazar reparse/symlink donde corresponda. No ocultar corrupción como un estado vacío nuevo. |
| SEC-006 | No publicar tokens, homes, ASAR, ejecutables oficiales ni builds parcheadas. El ASAR local contiene configuración de control sensible y no es un artefacto público. |
| SEC-007 | Preservar el firmado original y permitir únicamente el cambio de digest ASAR previsto en el escritorio derivado; mantener activos los fuses/checks de integridad. |
| SEC-008 | Conservar los registros, manifest, publisher, protocolo y componentes de la aplicación oficial; nunca hacerse pasar por un producto firmado por OpenAI. |

La ACL protege frente a otros usuarios normales, no frente a un proceso malicioso con la misma identidad ni frente a un administrador. El filtrado de entorno no es una allowlist de todas las variables. Los logs/estado nativos pueden contener datos de uso; la ausencia de prompts en los registros de decisiones del gateway no anonimiza toda la aplicación.

Fuentes: [modelo de seguridad](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/WINDOWS-SECURITY.md), [API control](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/control/server.go), [private filesystem](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/internal/securefs/secure_windows.go).

### Límites y observabilidad

Estado: Implementado · Cubierto · Histórico

El control expone gestión/login de cuentas, modo de routing, perfiles, resets, eventos y atribución. `/v1/thread-account` representa ownership; `/v1/thread-spending?threadId=...` representa la última aceptación; `/v1/spending` representa el historial efímero de decisiones terminadas. Ninguno constituye una factura.

| Canal | Límite/propiedad principal |
| --- | --- |
| Inferencia/compaction HTTP | 128 MiB de entrada sin compresión; exceso rechazado antes de gasto. |
| SSE upstream / respuesta compaction | Scanner hasta 32 MiB por línea SSE; respuesta compaction hasta 32 MiB. No confundir con el límite del cuerpo HTTP. |
| Preparación de voz | 2 MiB de cuerpo; SDP hasta 1 MiB. |
| Descubrimiento de modelos read-only | Respuesta acotada a 4 MiB. |
| Mensaje JSONL del app-server hijo | 64 MiB. |
| API de control | Cuerpo hasta 64 KiB; cabeceras hasta 16 KiB. |
| Native Messaging Chrome | Frame hasta 1 MiB; contexto recibido acotado. |
| Historial de decisiones de gasto | Últimos 100 intentos terminados, solo memoria. |

Los timeouts separan lectura de cabeceras, cuerpos y espera upstream; la espera de cabeceras de inferencia admite razonamiento/colas largos. No se añade un timeout global de escritura que interrumpa todo SSE. El cierre y cancelación deben liberar las reservas sin duplicar el gasto.

La observabilidad disponible es identidad, secuencia, política, resultado, duración y IDs acotados. En inferencias, el ID de hilo procede de `client_metadata.thread_id`; no se añaden los fallbacks de cabecera incorporados después. No hay en esta base atribución monetaria/tokens por turno ni calibración porcentual de gasto real.

El SSE de control leído por la UI está acotado a 256 KiB por evento/línea UTF-8, con reconexión progresiva y limpieza de readers/timers. No equivale al SSE upstream de hasta 32 MiB por línea. Health solo devuelve un `ok` fijo local y no requiere token; las rutas privadas sí.

### Diagnóstico conservador

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| OPS-001 | `doctor_windows.ps1` inspecciona procesos por ruta exacta bajo la app, versiones, listener, estado no secreto, almacenamiento y fragmentos de logs redactados. No inicia/detiene procesos ni lee OAuth, perfiles Chromium, conversaciones, credenciales MCP, command lines o dumps. |
| OPS-002 | Enviar el token al control solo después de validar que ambos manifests coinciden, el listener es loopback y su PID pertenece al árbol esperado. Si no puede comprobarse, usar diagnóstico no secreto. |
| OPS-003 | `measure_windows_router.ps1` observa contadores de procesos propios sin arrancarlos/cerrarlos: memoria, handles, procesos, picos y pendientes. Un soak breve no demuestra ausencia de fugas. |

El doctor no escribe por sí mismo un informe ni decide qué carpetas son seguras para borrar. Revelar rutas es opt-in y un informe redactado necesita revisión antes de compartirlo. Los umbrales por defecto del sampler son crecimiento final de 256 MiB de working set, 256 MiB de memoria privada, 1.000 handles y cuatro procesos; la aplicación de umbrales es explícita.

Fuente: [diagnóstico y resource soak](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/WINDOWS-DIAGNOSTICS.md).

## Compatibilidad

### Perfil exacto incorporado por c4eb2ea

Estado: Implementado · Histórico · Pendiente

| Componente | Referencia 26.924 |
| --- | --- |
| Plataforma objetivo | Windows 10/11 x64 |
| Paquete Store | `OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0` |
| ASAR / build | `26.924.22138` / `11645` |
| CLI oficial | `0.158.0-alpha.2.1` |
| ASAR original SHA-256 | `89fba67324ffb8dd54ccf13b6f097172e697549eeb1f26396f86f972c10c5b0c` |
| Runtime CUA / paquete | `0.0.24/20260924074400-f52ea85e2a98` / `@oai/cua 0.2.5` |
| Node del manifest / binario CUA | `24.21.0-cua.1` / `24.21.0` |
| Árbol CUA | 2.366 archivos, 251.062.420 bytes |
| SHA-256 árbol CUA | `355f5b4661571019ff76e671289320694585e6e3c5fabfddd27f77f6a48f8cb9` |

Los nueve perfiles exactos presentes en el patcher son los siguientes. No se extrapola su aceptación a 26.924 o a otra actualización.

| Paquete Windows | ASAR | Build |
| --- | --- | --- |
| 26.924.2738.0 | 26.924.22138 | 11645 |
| 26.917.6896.0 | 26.917.51856 | 10492 |
| 26.915.4065.0 | 26.915.31945 | 9922 |
| 26.911.7940.0 | 26.911.61220 | 9647 |
| 26.908.4834.0 | 26.908.40834 | 8881 |
| 26.903.9818.0 | 26.903.71938 | 8576 |
| 26.903.8094.0 | 26.903.61454 | 8378 |
| 26.901.6511.0 | 26.901.51231 | 8109 |
| 26.820.9563.0 | 26.820.71523 | 7226 |

La compatibilidad se identifica por versión Appx, versión/build internos, arquitectura, hash y anchors semánticos. No se aceptan rangos de versión o un nuevo desktop por compartir apariencia.

La fuente oficial se inventaría y verifica antes de parchear. El destino/staging deben respetar el presupuesto de rutas proyectadas del payload: menos de 260 unidades UTF-16. Ese guard no afirma que cualquier ruta fuente larga sea ilegible.

La copia contiene `ChatGPT.original.exe`, original firmado, y `ChatGPT.real.exe`, derivado al cambiar exclusivamente el campo digest de 64 caracteres del recurso `INTEGRITY/ELECTRONASAR`. **El derivado no conserva la validez Authenticode de OpenAI.** No se soluciona apagando fuses ni falseando la firma. CLI y helpers oficiales se preservan y verifican por separado.

Fuente: [compatibilidad histórica](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/COMPATIBILITY.md), [patcher exacto](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/scripts/patch_windows_app.py).

### Construcción, actualización y rollback

Estado: Implementado · Cubierto · Histórico

| ID | Contrato |
| --- | --- |
| LIF-001 | Descubrir OpenAI.Codex mediante Appx o fuente explícita; leer WindowsApps sin tomar posesión, cambiar permisos o desregistrar el paquete. |
| LIF-002 | Validar fuente/destino, perfiles, hashes, firmas y anchors exactos; rechazar un input desconocido por defecto. `--allow-untested-source` no omite anchors ni verificación estructural. |
| LIF-003 | Compilar o recibir binarios explícitos, copiar el payload a staging hermano, parchear/reempaquetar ASAR y conservar los módulos nativos unpacked. |
| LIF-004 | Instalar mux como `resources/codex.exe`, preservar CLI como `codex.real.exe`, instalar launcher y conservar los ejecutables oficiales correspondientes. |
| LIF-005 | Verificar coherencia de manifest, sidecar, puerto/token y árbol; parsear todos los bundles JS modificados antes de publicar. |
| LIF-006 | Desactivar el updater de la copia manteniendo la inicialización necesaria para resolver su política. Una actualización Store requiere otro perfil y reconstrucción, no sustituye silenciosamente el router. |
| LIF-007 | Publicar por intercambio de directorios en el mismo volumen, conservar backup y revertir si falla la publicación. Rechazar la sustitución de una copia en uso; no matarla desde el instalador. |
| LIF-008 | Mantener datos fuera del programa; rollback del programa no debe borrar cuentas ni pretende restaurar automáticamente un estado incompatible. |

Requisitos del build: Go **1.26.7** en `go.mod`, Node **22.12+**, Python **3.10+**, dependencia build `@electron/asar 4.3.0` bloqueada en npm. El backend Go no tiene dependencias runtime de terceros en esa revisión. PowerShell **7** es obligatorio para lifecycle y verificación completa; el modo `-RepositoryOnly` conserva la posibilidad de PowerShell 5.1.

La vía de uso es clonar y construir localmente desde código. Existe tooling portable y MSIX experimental, pero **no es necesario producir ni distribuir un instalador** para este alcance. Firma de distribución y VM limpia son gates propios del paquete, no sustitutos de recuperar el router local.

Fuente: [instalación del commit](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/INSTALL-WINDOWS.md), [lifecycle installer](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/scripts/install_windows.ps1), [rollback](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/scripts/rollback_windows.ps1).

## Estado y recuperación

### Evidencia disponible para esta base

Estado: Histórico · Cubierto · Pendiente

Esta revisión documental ha contrastado archivos del commit. **No ha ejecutado la app, inferencias, login, resets, audio o Computer Use ni los tests del router.** Las cifras siguientes son resultados históricos registrados en el commit, no resultados nuevos obtenidos durante esta redacción.

| Evidencia histórica 26.924 | Resultado registrado | Alcance |
| --- | --- | --- |
| Python Windows | 129/129 PASS | Tests del patcher/lifecycle/inventario y contratos. |
| JavaScript UI | 45/45 PASS | Componentes, estado y acciones sintéticas. |
| Release fuente | 9/9 PASS | Metadata/empaquetado fuente. |
| Go tests / vet | PASS | Núcleo y comprobación estática. |
| Smoke offline | 6 etapas PASS | Integración sin cuentas ni gasto real. |
| Bundles JS modificados | 13/13 PASS | Parseo del ASAR real de referencia. |
| Menú empaquetado | PASS | Rename, routing, Usage, resets scoped, perfil/plugins y carga lazy. |
| App-server real passthrough/router | PASS | Homes temporales sin cuenta; initialize, initialized, account/read y thread/list vacío; cierre EOF. |
| CUA/Appshots | Contratos estáticos | No captura/interacción de escritorio real. |
| Voz con audio | Pendiente | No aceptación E2E registrada para este candidato. |

La prueba histórica de dos cuentas e inferencias Astra con CLI 0.153.4 pertenece a otra versión anterior. Es antecedente de la arquitectura de gasto, **no aceptación live del CLI 0.158.0-alpha.2.1 ni de Codex 26.924**. Los informes antiguos de release/E2E tampoco certifican automáticamente este commit.

Fuentes: [evidencia 26.924](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/UPGRADE-26924-PLAN.md), [smoke tests](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/tests/windows/SMOKE-TEST.md), [informe E2E histórico](https://github.com/TheDaniXSX/codex-subscription-router-windows/blob/c4eb2eae4fc5c59987a901d373f7b58f5015c3a9/docs/E2E-REPORT-WINDOWS.md).

### Divergencias documentales y límites que no se deben perder

Estado: Histórico · Pendiente

| ID | Diferencia o límite en la propia base | Interpretación para recuperarla |
| --- | --- | --- |
| DIF-001 | La arquitectura histórica y parte del README describen puntuación por hilo, sticky/failover y desempates antiguos. El launcher habilita gasto por petición. | Aplicar ROU y separar la rama legado. No reinstaurar failover silencioso. |
| DIF-002 | Algunas instrucciones históricas de actualización usan `powershell`, pese al requisito de PS7 incorporado. | Usar `pwsh`; no relajar el guard de lifecycle. |
| DIF-003 | Un informe E2E o metadata release de 0.2.0 mantiene referencias a candidatos antiguos. | Repetir los gates contra la revisión y payload exactos antes de declarar recuperación. |
| DIF-004 | “Last inference” y “Last request” observan momentos distintos: finalización global frente a aceptación por hilo. | Mantener ambos conceptos; ninguno representa gasto monetario exacto. |
| DIF-005 | Cuota específica por modelo no guía la selección Auto de esta base. | Mostrar el límite y devolver el rechazo upstream sin gastar otra cuenta. |
| DIF-006 | Cuota combinada y skills combinadas son sumas, no normalización ni unión de IDs. | No presentar porcentajes/contadores como magnitudes equivalentes por plan. |
| DIF-007 | Chrome tiene implementación propia opt-in, pero su consumo final por el escritorio y su release E2E siguen abiertos. | No venderlo como paridad nativa ni tocar el registro oficial. |
| DIF-008 | CUA y voz cuentan con transporte/payload comprobables, sin aceptación completa del desktop exacto. | Verificar cada capacidad antes de prometer funcionamiento perfecto. |
| DIF-009 | La modificación legítima del digest invalida la firma del escritorio derivado. | Conservar original firmado y verificación estricta; no atribuir fallos sin evidencia. |
| DIF-010 | El commit fue elegido como referencia previa a analítica/PROD-DEV, no probado aquí como libre de errores. | Usarlo como baseline de comparación, no como garantía ni autorización de rollback. |
| DIF-011 | README/paridad escriben 500 hilos por cuenta, pero el código consulta hasta 2.000 y limita a 500 la página combinada. | Mantener la distinción entre máximo consultado y tamaño de página. |
| DIF-012 | Un scope de plugins inválido puede conservar su marcador y caer a routing habitual. | No prometer rechazo scoped estricto universal; probar la rama y decidir su corrección posteriormente. |
| DIF-013 | El perfil backend puede marcar resultado parcial, pero la UI no garantiza aviso visible de esa parcialidad. | No presentar una suma incompleta como completa; falta aceptación de esa rama. |
| DIF-014 | El backend de cuota agrupada no utiliza la misma admisión corta/larga/fresca que el gateway; la UI suma restantes. | El resumen pooled no prueba que una inferencia concreta tenga capacidad admisible. |

Estas diferencias no demuestran la causa de los problemas actuales. Para diagnosticar esa causa se necesita comparar por separado la instalación efectiva y los cambios posteriores con esta especificación.

### Criterios para aceptar una recuperación

Estado: Pendiente

Esta matriz expresa lo que se comprobaría en un trabajo posterior. No se han aplicado cambios ni ejecutado estas pruebas durante la redacción.

| ID | Aceptación observable | Prioridad |
| --- | --- | --- |
| ACC-001 | Binarios/ASAR/manifest proceden del mismo perfil exacto; oficial intacto y rollback recuperable. | Núcleo |
| ACC-002 | Inicio independiente y cierre limpio, sin procesos huérfanos, reinicios forzados ni contaminación de perfil/cachés. | Núcleo |
| ACC-003 | Login, Rename, habilitar/deshabilitar, logout y retirada mantienen las cuentas/identidades correctas. | Núcleo |
| ACC-004 | Cambiar suscripción entre dos peticiones del mismo hilo cambia la cuenta de inferencia, no el dueño del historial. | Núcleo |
| ACC-005 | Continuaciones y subagentes nativos obedecen la selección vigente en cada reserva. | Núcleo |
| ACC-006 | Modo explícito agotado/desconectado no gasta de otra cuenta; Auto reevalúa sin replay de entrega incierta. | Núcleo |
| ACC-007 | Environment muestra la última aceptación del hilo, incluso con concurrencia, reinicio y cambios de cuenta; vacío honesto cuando no hay evidencia. | Núcleo |
| ACC-008 | Menú lazy, Rename, Usage remaining, picker de resets, perfil/plugins funcionan al navegar directamente y sin clics perdidos. | Interfaz |
| ACC-009 | Apps/MCP y resets operan sobre su cuenta seleccionada, sin mutación accidental de Primary. Los previews no consumen créditos. | Interfaz |
| ACC-010 | Voz establece una llamada con audio y herramientas esperadas; cambio de modo no rompe la llamada existente. | Capacidad si se requiere |
| ACC-011 | Computer Use captura e interactúa con el entorno permitido sin registrar ni modificar componentes oficiales. | Capacidad si se requiere |
| ACC-012 | Integraciones Chrome/shell/icono se verifican en sus superficies reales y sus opciones permanecen opt-in. | Integración si se requiere |

Una recuperación proporcional comenzaría por aislar el código y artefactos de referencia, guardar la instalación/datos actuales, verificar el núcleo, adaptar solo los puntos de compatibilidad necesarios y activar con backup tras cerrar voluntariamente el router. **No basta hacer checkout de `c4eb2ea` sobre trabajo local ni sustituir binarios de otra versión.** Si la fuente oficial 26.924 ya no está disponible, hace falta un artefacto verificable o una adaptación explícita al nuevo payload; ambas decisiones van fuera de esta redacción.

### Mantenimiento de este documento y del visor

Estado: Implementado

`SPECIFICATION.md` es la fuente única; `index.html` es una vista generada, autocontenida, con índice, búsqueda, filtros y tema. Las fuentes técnicas enlazan al SHA completo, no a `main` ni a archivos cambiantes del checkout.

```text
node build.mjs
node --test test-portal.cjs
```

Se mantiene esta especificación congelada: cualquier ampliación funcional o actualización debe documentarse en otra revisión identificada, con su comparación y evidencia. No se insertan features nuevas dentro de la definición de `c4eb2ea`.

La documentación y el visor no modifican la app ni autentican cuentas. Las comprobaciones del visor validan renderizado/sanitización/interacciones sintéticas, no el funcionamiento del router. Véase [guía del visor](README.md) y [validación de esta entrega](VALIDATION.md).
