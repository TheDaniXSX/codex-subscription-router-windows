# Consumo por petición y calibración de cuota: plan de implementación

Estado: **implementación DEV autorizada y en curso**.
Decisiones de producto confirmadas el 2026-09-28. Rama: `codex/usage-calibration`.
Baseline revisado y actualizado desde GitHub: `c4eb2eae4fc5c59987a901d373f7b58f5015c3a9`.

## 1. Resultado esperado

A la derecha de `Worked for …`, cuatro controles compactos con iconos SVG:

| Icono | Dato / acción | Detalle accesible |
| --- | --- | --- |
| Diana con marca | Incluir en calibración; apagado inicialmente | Estado persistido, reversible; confirmación de ausencia de consumo externo desconocido |
| Medidor | Cuota observada | Lecturas inicial/final, sus horas, ventana, intervalo y concurrencia; asterisco si el consumo es compartido |
| Calculadora | Cuota estimada | Entrada, caché, salida, modelo, Fast, plan, versión del estimador y evidencia disponible |
| Moneda con `$` | Coste API equivalente | USD, desglose de tokens y tarifas verificadas con fuente y versión |

Los iconos llevan nombre accesible y tooltip; el estado no depende solo del color.
El nuevo grupo es hermano del botón que expande `Worked for`, nunca un botón
anidado dentro de otro. En ancho reducido pasa a una segunda línea a partir del
mismo punto de inserción; los datos y controles siguen accesibles por teclado.

La unidad visible es **una petición del usuario / turno raíz**, con todas sus
inferencias, continuaciones, compactaciones y subagentes descendientes. Una
inferencia HTTP no equivale a una petición del usuario. El despliegue de detalle
presenta un árbol: turno raíz → agentes → inferencias/operaciones. No incluye
conversaciones completas ni peticiones anteriores del mismo chat.

Cada nivel muestra hora de comienzo y finalización, cuenta y modelo, tokens,
API USD, estimación, lecturas de cuota, rango de medición y peticiones simultáneas.
Las horas de ejecución y de lectura de cuota son campos distintos. Se almacenan
en UTC y se muestran en la zona del usuario. Una duración no usa el reloj de pared
cuando hay un reloj monotónico disponible.

## 2. Baseline y límites verificados al elaborar el plan

Esta sección describe el punto de partida anterior a la implementación. El
estado de la entrega DEV y las diferencias respecto al diseño están en la
sección 13 y en `USAGE-CALIBRATION-DEV.md`.

- Paquete instalado: `OpenAI.Codex 26.924.2738.0`; ASAR `26.924.22138`, build `11645`.
- El manifest instalado coincide con esos valores; los procesos observados usan
  la instalación de `Codex Subscription Router`. Es evidencia de identidad de la
  instalación, no aceptación visual o funcional de esta propuesta.
- El soporte nuevo comienza en 26.924. Cada actualización futura exige adaptar y
  probar el perfil correspondiente. Las versiones anteriores conservan su
  comportamiento existente, sin prometer la nueva interfaz.
- `internal/spend/gateway.go` registra actualmente resultado, cuenta e IDs acotados;
  todavía no modelo, usage, lecturas de cuota ni asociación completa de agentes.
- `internal/mux/spending.go` mantiene 100 registros en RAM. Sus candidatos de
  enrutamiento reducen dos ventanas a un mínimo y pueden reutilizar una caché de
  dos segundos: ese mínimo no es una medición de consumo.
- `ui/thread-subscription.js` y `internal/state/thread_spending.go` muestran y
  persisten la última cuenta del chat. No bastan para el historial por turno.
- `scripts/windows_renderer_26924.py` usa un perfil exacto y menú lazy; el footer
  nuevo debe tener integración y pruebas propias, sin depender de abrir el menú.
- El installer admite destino, estado y puerto independientes. El launcher
  todavía conserva ciertos overrides heredados y una identidad visual fija:
  hace falta completar el aislamiento antes de ejecutar DEV junto a PROD.

Ancla 26.924 localizada en la instalación: `collapsed-turn-disclosure-61dac5311725.js`,
función `y(e)` para la etiqueta y `b(e)` para la fila. `gc(e)` en
`local-conversation-turn-543a8ec414f6.js` conoce conversationId/turnId y actividad
de agentes; hay que pasar esa identidad a través de
`conversation-blocks-8e6b09dae28b.js`. La UI nativa relaciona hijos directos con
`parentTurnKey` y conversationId. El gateway reenvía `X-Codex-Parent-Thread-Id`,
pero no lo persiste. Esas relaciones son puntos de integración observados;
la asociación transitiva y el ciclo de vida de agentes separados se cualifican
en M2. Estos nombres pertenecen exclusivamente al perfil exacto 26.924.

## 3. Modelo de datos y correlación

Almacén propio `usage-ledger.sqlite` bajo el StateRoot de cada instalación,
independiente de `state.json` y de las bases de conversaciones oficiales.
Elegir un driver SQLite de Go puro, fijar versión y revisar licencia/dependencias
antes de incorporarlo; no introducir una dependencia de DLL o CGO no cualificada.

Entidades:

1. `root_turns`: ID estable, threadId/turnId nativos, inicio/fin, estado de cierre,
   cobertura, selección manual y revisión. Máximo 5.000 raíces finalizadas.
2. `agents`: parentAgentId, rootTurnId, ID nativo, evidencia de asociación; árbol
   completo con protección contra ciclos y alias duplicados.
3. `inferences`: UUID, raíz/agente, operación, intento, responseId si existe,
   cuenta, plan observado, modelo solicitado/servido, tier solicitado/servido,
   inicio/fin, outcome, uso presente/ausente/parcial y datos de coste.
4. `quota_samples`: cuenta, limitId/bucket, duración, resetAt, comienzo/fin de la
   consulta, usedPercent original, procedencia, precisión conocida/desconocida.
5. `observation_groups` y miembros: una sola etiqueta de consumo para un conjunto
   completo de inferencias; referencias a snapshots, revisión y calidad.
6. `calibration_events` y `model_versions`: selección/desmarcado, features,
   algoritmo, parámetros, rango temporal y conjunto de muestras usado.
7. `predictions`: estimación realizada al finalizar con usage disponible, antes
   de entrenar con esa misma observación; modelo, intervalos y cobertura.

Capturar `threadId + turnId` y relaciones padre/hijo de notificaciones nativas,
metadatos de inferencia y headers cualificados. La relación con el turno raíz
debe sobrevivir al reinicio y al cambio de cuenta durante un turno. Los agentes
anidados se recorren por identidad, contando cada operación una sola vez.
La marca booleana `subagent` y la cercanía temporal no prueban parentesco.

Hito técnico obligatorio: confirmar en 26.924 qué notificación/campo transporta
el vínculo de agente y turno, y pasar el ID correcto al componente del footer.
Si falta, conservar el registro como `sin asociar`, mostrar cobertura parcial y
deshabilitar el entrenamiento afectado. No atribuirlo al último chat visible.
Si un agente continúa después de la respuesta del padre, el total sigue como
provisional hasta que cierre ese trabajo; un timeout por sí solo no prueba cierre.

Los eventos de transporte y los acumulados de tokens del app-server pueden
describir el mismo gasto. Se elige una fuente autoritativa por operación y se
deduplica; jamás se suman ambos. Los reintentos físicos genuinos son operaciones
separadas, incluso si el turno visible es el mismo.

## 4. Captura y coste API

Extraer solo campos permitidos de uso y metadatos; no persistir prompts, respuestas,
razonamiento, outputs de herramientas, eventos SSE completos ni credenciales.
Los contadores ausentes son null, no cero. Conservar usage válido aunque el
resultado sea fallido, incompleto, cancelado o de compactación. Su precisión y
cobertura determinan qué se puede calcular y entrenar.

Para Responses: input, cached input, output y razonamiento cuando existan. El
razonamiento incluido en output no se vuelve a sumar. Entrada ordinaria es
`input - cached` cuando el esquema y ambos campos lo permiten. Nuevas categorías,
por ejemplo cache writes o uso multimodal, requieren su adaptador explícito.

El parser SSE acumula los campos data hasta el separador de evento, admite
fragmentación/múltiples líneas y limita memoria. Reenvía los bytes originales;
el scanner actual normaliza CRLF, por lo que se probará también ese contrato al
modificarlo. Un problema de telemetría nunca reintenta una inferencia aceptada.
Se mantiene la semántica de cancelación: no prolongar generación tras cancelar
únicamente para buscar su usage final.

Crear adaptadores para cada fuente que entregue uso cualificado: Responses,
compact JSON, errores con usage, voz y herramientas multimodales cuando su uso
sea visible. Una llamada de setup de voz no acredita los minutos de la sesión;
WebRTC o herramientas fuera del gateway pueden ser inobservables. En esos casos
se registra cobertura desconocida/parcial. Si existe usage pero falta tarifa,
se conservan los tokens y el coste queda no disponible.

El catálogo de tarifas contiene modelo exacto, tier API, categorías, umbrales
de contexto, fecha efectiva, fuente y versión. Capturar cache writes/modalidades
cuando hagan falta para la tarifa. USD se guarda con precisión decimal fija.
El coste completo solo se etiqueta completo si se conocen todos sus componentes;
las sumas parciales dicen `al menos` o `parcial`, sin reemplazar ausencias por cero.

**Fast de suscripción y tier de API son conceptos distintos.** El multiplicador
2,5 de suscripción no se aplica automáticamente a la columna API USD. Para esa
columna se usa la tarifa API verificada del tier que se está comparando; si solo
hay correspondencia Standard, se etiqueta `equivalente API Standard`. Los precios
históricos y sus versiones quedan congelados.

## 5. Observado, solapamiento y muestras agrupadas

Medir ambas ventanas disponibles por separado, usando sus duraciones reales y
limitId. `observado_pp = usedAfter - usedBefore`; mostrar también el restante
inicial/final si ayuda a leer la UI. Un 80% → 79% restante son 1 pp consumidos.

Tomar baseline antes de enviar trabajo y lectura nueva después. La medición
usa consultas explícitas, no la caché de candidatos. El camino normal reutiliza
una lectura ya en curso con procedencia conocida o toma una nueva con timeout
acotado; si falla, la inferencia sigue y la medición queda incompleta. La espera
adicional de baseline tendrá un presupuesto máximo inicial de 1 s, ajustado con
el benchmark de metadata. Tras finalizar, lecturas en segundo plano a 0, 2, 5,
10 y 20 s, agrupadas por cuenta, sin bloquear la respuesta al usuario.

Dos lecturas locales distintas no garantizan que el servidor haya actualizado
su contador. Conservar todas las observaciones útiles de ese intervalo y un
estado `pendiente / asentamiento heurístico / inválido`. No declarar precisión
exacta por ver dos valores iguales. Si no hay cambio, mostrar `0 pp observado`
con nota de resolución/posible retraso; no tratarlo como cero real ni entrenar
una pendiente cero. Solo usar un intervalo censurado cuando la resolución esté
sustentada; de otro modo unir intervalos contiguos hasta obtener señal útil o
dejar esa muestra pendiente. No seleccionar solamente las muestras positivas
de una serie cuantizada, lo que sesgaría la calibración.

Concurrencia conocida:

- Construir por cuenta y bucket grupos de intervalos conectados desde el baseline
  hasta el snapshot final, incluyendo las colas de actualización. Si otra llamada
  comienza durante ese intervalo, forma parte del grupo, aunque la primera haya
  terminado de transmitir.
- Para A+B solapadas, la etiqueta es una sola variación de cuota y las features
  son `tokens(A)+tokens(B)`. La bajada completa nunca se adjudica a cada una.
- Evitar etiquetas con intervalos de medición superpuestos. Unir grupos si fuera
  necesario; las muestras consecutivas pueden compartir un snapshot frontera,
  pero no el mismo tramo de consumo. Contabilizar cada etiqueta una vez por bucket.
- Si hay tráfico continuo, el grupo puede quedar abierto. Al agotar el tiempo de
  observación, conservar pendiente; no retrasar ni serializar las peticiones para
  forzar una medición. Una nueva ventana válida puede cerrarse posteriormente.
- Un grupo solo entrena si todas las contribuciones conocidas tienen features
  utilizables y todas las raíces participantes están marcadas. Uso parcial o una
  operación desconocida bloquean ese grupo, sin perder sus datos visibles.

En cada agente/petición afectada se muestra `observado X* — compartido con …`,
con los IDs, horas y scope del grupo. Los totales deduplican observaciones. Si el
grupo incluye otro turno ajeno, el footer muestra `X* del intervalo compartido`;
su estimado y API siguen siendo propios del turno. No existe un observado
individual recuperable por dividir según el estimado.

El toggle raíz está OFF. Activarlo confirma que no hubo gasto externo desconocido
en sus intervalos. Para un grupo con varias raíces, el detalle ofrece seleccionar
explícitamente todas las raíces conocidas en una operación atómica; también
permite desmarcar una. No modifica silenciosamente las otras peticiones.
Cambio de miembros después de confirmar invalida esa confirmación de grupo y
requiere revisión de la nueva composición. Una inferencia fallida con usage
autoritativo completo puede ser parte del entrenamiento.

Cambios de reset/plan/bucket, valores hacia atrás o lecturas inválidas invalidan
el delta entre esos puntos. El reset normal **no borra** coeficientes ni muestras
históricas de un mismo régimen; solo impide restar porcentajes de épocas distintas.

## 6. Varias cuentas y cobertura

USD se suma por operaciones únicas de todo el árbol. Tokens se conservan por
modelo/categoría además del total. Decisión final del usuario: **todos los
porcentajes mostrados y los objetivos del calibrador usan cuota equivalente
Pro ×20**, también al consumir una cuenta ×1 o ×5.

`pp_Pro20 = pp_originales × capacidad_cuenta / 20`

Plus ×1 divide entre 20; Pro ×5 divide entre 4; Pro ×20 conserva el valor.
Las lecturas originales, capacidad aplicada y versión de la conversión se
conservan en el detalle. La etiqueta es `Observado · ×20`, con su normalización
explicada. Solo se suman contribuciones únicas normalizadas del mismo bucket;
5 h y semana permanecen separados. Una cuenta de capacidad desconocida produce
un resultado parcial, nunca una capacidad inventada. API USD no cambia.

Un marcador `*` expresa solapamiento conocido. La UI explica que su ausencia no
demuestra ausencia de gasto en otro PC, cloud, ChatGPT Work u otro proceso. Durante
el desarrollo, la producción anterior es también un observador externo si usa la
misma suscripción: abrir ambas apps es compatible, pero sus peticiones aún no
comparten un registro completo. Las pruebas de calibración deben usar cuentas
distintas o mantener sin uso esa cuenta en PROD durante cada intervalo elegido.
No importar inferencias ficticias a partir de los últimos 100 outcomes de PROD.

## 7. Estimador y detección futura de cambios

Features: tokens de entrada ordinaria, cacheada y salida por modelo y tier,
categorías extra si existen, plan al ejecutar, ventana y cuenta. Guardar también
contexto relevante (por ejemplo longitud de contexto y esfuerzo) para investigar
residuales, sin crear desde el inicio un coeficiente libre para cada combinación.

Prior relativo: proporciones de la tabla API Standard verificada. Fast comienza
con multiplicador 2,5 para modelos donde esté documentado (otros usan su valor
oficial). Capacidad inicial Plus=1, Pro5=5, Pro20=20: el porcentaje esperado para
igual trabajo se **divide** entre la capacidad, no se multiplica. Las desviaciones
de Fast, categorías, modelos y plan podrán aprenderse, sin imponerlas como verdad.

Forma inicial conceptual para bucket b en la unidad visible Pro ×20:

`E[pp_Pro20] = escala_b × sum(tokens_categoria × peso_modelo_categoria × factor_tier)`

La escala se ajusta exclusivamente a deltas observados normalizados a ×20. La
capacidad solo convierte la lectura nativa; no se divide otra vez al predecir.

Las tarifas dan proporciones, pero **no dan la escala absoluta tokens → pp**.
Sin ninguna observación utilizable, mostrar unidades relativas de consumo y
`% pendiente de calibración`. Con la primera observación independiente se puede
ajustar una escala provisional (incertidumbre alta); no inventar pp desde dólares.

Implementar por etapas: escala escalar no negativa; después regresión no negativa
regularizada hacia los pesos iniciales cuando la matriz tenga suficiente rango
y diversidad. Por grupo g, predecir la suma de sus contribuciones, no crear labels
individuales. Entrenamiento robusto y versionado; no introducir interceptos
libres por cuenta, plan y modelo simultáneamente porque no son identificables.
Fijar una referencia para la escala. Permitir efectos aprendidos solo cuando
comparaciones entre modos/planes/categorías los identifiquen; mientras tanto
marcarlos `supuesto`, aunque haya muchas muestras repetitivas.

El grupo mínimo de entrenamiento es cuenta + bucket + régimen compatible;
comparaciones entre cuentas se normalizan por plan como hipótesis y conservan
efectos/errores por cuenta. Modelos/tarifas/planes desconocidos no reciben un
multiplicador supuesto de uno. Fast solicitado y efectivo se guardan separados;
si solo se conoce el solicitado, la estimación es provisional y no valida Fast.

Incertidumbre: número de grupos independientes, variedad/rango de features,
residuales, resolución de cuota, soporte temporal y extrapolación. No se obtiene
confianza alta solo por superar cinco muestras. Una muestra con muchos subagentes
sigue aportando una etiqueta por ventana, no decenas de etiquetas independientes.

Congelar el estimado disponible al completar el uso, antes de incorporar su label.
Desmarcar elimina el grupo del entrenamiento actual y reconstruye el modelo;
no reescribe el observado, tarifa o predicción histórica. Una reproducción con el
modelo actual se expone como dato distinto. Guardar el conjunto/versión/fecha de
entrenamiento para poder analizar deriva sin que el modelo oculte su propio error.
Las gráficas y alertas automáticas de cambios de consumo quedan para una fase
posterior; esta entrega conserva sus entradas y exportación local de metadatos.

## 8. Retención, privacidad y dimensionado

- 5.000 turnos raíz finalizados con todos sus descendientes; sin caducidad por días.
  Activos se conservan hasta terminar; se cuenta aparte su ocupación.
- Presupuesto orientativo, a medir con fixtures: 5.000 raíces × 20 inferencias =
  100.000 operaciones. Con 1–2 KiB por operación más índices/snapshots/versiones,
  prever cientos de MiB y espacio para WAL/migraciones. No es un tamaño medido.
- No cargar todo el historial en RAM ni mandarlo entero al footer. Paginación por
  cursor (50 por defecto, máximo 200), agregados por raíz y detalle bajo demanda.
- Evicción transaccional de las raíces finalizadas más antiguas al exceder 5.000;
  nunca truncar hijos de una raíz conservada ni borrar silenciosamente por tamaño.
- Las observaciones compartidas cuyos miembros se evictan se conservan como
  evidencia referenciada de los turnos aún retenidos, pero dejan de entrenar si
  ya no pueden reconstruirse y desmarcarse todos sus miembros. Las predicciones
  congeladas permanecen. Borrar datos muestra qué muestras dejan de contribuir.
- El historial de calibración derivado se reconstruye de lo retenido; el límite
  de 5.000 limita también el horizonte de futuros análisis. Ofrecer exportación
  local antes de borrar; no crear archivo histórico ilimitado implícito.
- Almacén privado con ACLs y WAL/SHM protegidos, IDs opacos, sin correo, credenciales
  ni contenido de conversaciones. GitHub recibe código/esquemas/fixtures sintéticos.
- Migraciones transaccionales y versionadas con backup consistente; versión nueva
  desconocida o corrupción se conserva y se comunica. Un error de disco no rompe
  ni reintenta inferencias; UI indica telemetría perdida/no guardada. El toggle se
  confirma solo tras persistencia. Reconstrucción del modelo publica otra revisión.

## 9. API local y actualizaciones de interfaz

Mantener `/v1/spending` compatible. Nuevas rutas bajo `/v1/usage`:

- `GET /turns?threadId=...&turnId=...` y listado paginado.
- `GET /turns/{id}`: árbol, totales, cobertura, mediciones y calibración.
- `PATCH /turns/{id}/calibration`: bool + revisión esperada.
- `PATCH /observation-groups/{id}/calibration`: selección explícita de miembros
  con revisión esperada, para confirmar un conjunto simultáneo conocido.
- `GET /models`: parámetros, priors, muestra efectiva, incertidumbre y versiones.
- Exportar/borrar historial y resetear selección requieren endpoints separados.

Todas las rutas conservan autenticación loopback por header y política de origen.
El renderer nunca envía valores de tokens, cuota ni precio para aceptarlos como
mediciones. Validar estrictamente JSON, tamaño e identidades. Updates concurrentes
usan revisión; conflicto devuelve estado vigente. Eventos llevan IDs/revisión y
permiten recuperar por GET después de desconexión, sin aplicar versiones antiguas.

El resumen inicial llega sin esperar el polling de cuota. Estados: trabajando,
uso recibido, observación pendiente, completo, parcial y persistencia fallida.
Cambiar de chat cancela consultas previas; un evento de otro turno nunca modifica
la fila actual. La medición tarda lo que necesite dentro del presupuesto y no
mantiene abierto artificialmente `Worked for`.

## 10. Producción y desarrollo simultáneos

Repositorio de producción permanece en `main`; desarrollo usa worktree separado
y rama `codex/usage-calibration`. Las pruebas que escriben archivos usan ese
worktree o un workspace de fixtures, no el proyecto abierto por PROD.

| Recurso | Producción | Desarrollo previsto |
| --- | --- | --- |
| App | instalación actual `Programs/Codex Subscription Router` | `%LOCALAPPDATA%/CSR-Dev/app` |
| Estado | `Programs/Codex Subscription Router Data` | `%LOCALAPPDATA%/CSR-Dev/data` |
| CODEX_HOME | home actual | `%LOCALAPPDATA%/CSR-Dev/data/PrimaryHome` |
| CODEX_SQLITE_HOME | valor actual | el mismo `PrimaryHome` privado de DEV |
| Perfil/caché/logs | actuales | todos bajo DEV data |
| Control e inferencia | puertos/tokens actuales | puertos loopback propios, token nuevo |
| Identidad de app | identidad actual | `com.openai.codex.subscription-router.dev`, icono y título DEV |
| Acceso directo | actual | `Codex Subscription Router [DEV]`, creado de forma independiente |
| Cuentas | actuales | login propio en DEV; configuración nueva y editable |
| Actualización/rollback | ciclo actual | scripts explícitamente limitados a DEV |

Implementar un canal `dev` en instalador, sidecar de launcher y manifest; producción
conserva el default. El launcher DEV fija homes y elimina overrides heredados que
apunten a PROD (estado, sqlite, caché, auth store, perfil). Login dev usa almacenamiento
de credenciales local del home para evitar colisión de keyring. Comprobar rutas
resueltas/reparse: app, datos, homes y backup DEV nunca pueden contener a PROD ni
ser contenidos por ella. Registrar fingerprint y commit del build en la UI DEV.

Separar AppUserModelID, agrupación de taskbar, título, shortcut y bloqueos de
ejecución. Mantener la exclusión global de instalaciones cuando proteja recursos
compartidos; esta exclusión no debe impedir que PROD y DEV se ejecuten a la vez.
El updater del paquete derivado permanece desactivado; DEV se reconstruye desde
la rama. Protocolos y registro de Chrome oficiales no se registran/reasignan desde
DEV. Cualquier wrapper nuevo valida la identidad de destino antes de reemplazarlo.

Primer instalador DEV en `NoLaunch`, seguido de verificación de artefactos/paths;
solo entonces lanzar DEV. El instalador actual sin más parámetros no es un comando
seguro de desarrollo: falta fijar homes e identidad. No se copia la base de chats
de PROD en vivo ni se comparten archivos OAuth. El usuario puede iniciar sesión
normalmente en DEV. Compartir suscripción sigue compartiendo cuota del servidor.

La reversión cierra únicamente DEV y vuelve a su backup conservando sus datos.
Promocionar la función a producción es un paso posterior al PR aceptado y a la
cualificación; requiere un cierre voluntario de la app al sustituir sus binarios.
Crear una rama o un build DEV no prueba convivencia real de ambas aplicaciones.

## 11. Entregas y pruebas en GitHub

La rama comienza con este documento. Implementar commits revisables en orden:

| Hito | Entrega | Prueba que habilita el siguiente hito |
| --- | --- | --- |
| M1 | Canal DEV, homes/identidad/puertos/paths aislados, installer/rollback | Dos apps simultáneas; DEV no modifica archivos estables, procesos ni integraciones de PROD |
| M2 | Contrato real 26.924 de turno/agentes/usage/tier/cuotas | Fixtures sintéticos de árbol anidado y terminales; ensayo real acotado en DEV confirma correlación |
| M3 | Ledger SQLite, ids, migración, API/eventos y retención | Reinicio, concurrencia, atomicidad, fallos de disco y 5.001 raíces completas |
| M4 | Adaptadores usage, catálogo API y observaciones agrupadas | Costes exactos para fixtures; solapamientos deduplicados, ventanas/resets, retraso/precisión |
| M5 | Priors, escala, regresión y selección manual | Recuperación sintética, colinealidad, Fast/planes, opt-in/out, independencia y ausencia de fuga temporal |
| M6 | Cuatro iconos en Worked for, árbol de detalle y estados | Contratos de ASAR 26.924, montaje UI, teclado, navegación, layout real y overflow |
| M7 | Cualificación, docs, CI del SHA final y piloto | Gates de repositorio + evidencia visual y real, errores y límites documentados |

Matriz mínima de aceptación:

1. Padre + hijos + nietos: uso/API sumado exactamente una vez, agente desprendido,
   cambio de cuenta, metadata ausente, replay y respuesta fuera de orden.
2. A y B simultáneas, luego C antes del snapshot final: un grupo A+B+C y un delta;
   una raíz sin marcar bloquea entrenamiento; marcar conjunto permite entrenar;
   desmarcar elimina su influencia. Intervalos contiguos no duplican consumo.
3. Solapamiento en otra cuenta no contamina; ×1/×5/×20 con el mismo trabajo
   producen el mismo consumo equivalente ×20; multi-bucket no suma 5 h + semana.
   Lecturas nativas preservadas y consumo desconocido señalado.
4. Falta usage/tarifa/cache/tier; failed/incomplete con usage; voz setup sin consumo;
   compactación, cancelación, error de metadata; nunca convertir desconocido en cero.
5. SSE fragmentado, multiline, CRLF, duplicado, truncado y evento grande; passthrough
   comprobado y ausencia de payloads sensibles en ledger/logs/exportación.
6. Ventana reseteada/plan cambiado, atraso de cuota, lecturas iguales y cuantización:
   no división por cero, no pendientes falsas, no entrenamiento selectivo sesgado.
7. Datos sintéticos identificables recuperan coeficientes dentro de tolerancias
   declaradas para el ruido; datos colineales no aparentan precisión. Fast 2,5 y
   divisores 1/5/20 son priors comprobables; escenario alterado aprende desviaciones.
8. Predicción congelada anterior al entrenamiento propio; evaluación cronológica
   y de grupos independientes; no usar las dos ventanas como doble evidencia para
   una sola métrica. Sin escala inicial no hay porcentaje ficticio.
9. Retención sin edad: una fila antigua dentro de las 5.000 persiste; 5.001 evacúa
   raíz completa, preserva hijos de las restantes y resuelve referencias compartidas.
10. Endpoints con auth/origin/JSON/revisión inválidos, cambio concurrente, evento
    viejo, error al guardar, navegación rápida y recuperación después de reiniciar.
11. Coexistencia: dos roots, homes, sqlite/profile y listeners distintos, shortcut
    DEV inequívoco, login y stop/rebuild DEV no afectan PROD. Comparar hashes de
    binarios/config estáticos, no exigir hashes inmóviles de logs/DB de PROD activa.
12. Escala: 5.000 raíces × 20 operaciones y caso de fanout alto; medir disco/RAM,
    duración de consultas y recálculo. El trabajo de telemetría va fuera del flush
    crítico; objetivos iniciales P95 consulta de resumen <100 ms y procesamiento
    local por evento <5 ms bajo fixture nominal, reportando equipo y distribución.

Checks: suites focales anteriores, `npm run check:windows`, `npm run smoke:windows`,
`npm run release:check`, race tests donde CI los requiere; actualizar la cobertura
de scripts nuevos y validación del driver SQLite. Verificar bundles modificados,
contratos empaquetados y cualificación de integridad en el ASAR exacto.
Los tests reales se ejecutan en DEV con consumo acotado y registrado; el piloto
aprovecha trabajo normal y confirma selección del usuario para calibrar.

PR en borrador desde la rama, sin merge automático. CI corresponde al SHA de la
rama; la publicación contiene fuente y evidencias saneadas, nunca app oficial,
ASAR, ejecutables, DB de uso, credenciales ni capturas con identidades. Verificar
SHA remoto tras cada push relevante. Actualizar checklist de Windows con el
footer, aislamiento DEV y cálculos. No crear tag/release solo por esta función.

## 12. Fuentes y hechos pendientes de cualificación

Fuentes oficiales consultadas el 2026-09-28:

- <https://learn.chatgpt.com/docs/pricing>: planes Pro 5x/20x; consumo compartido;
  tarifas de créditos y advertencia de que no determinan por sí solas la cuota.
- <https://learn.chatgpt.com/docs/agent-configuration/speed>: Fast 2,5 para GPT-6
  donde disponible; excepciones por modelo y distinción frente a tarifas API.
- <https://developers.openai.com/api/docs/pricing>: tiers, cache writes y umbrales
  de contexto que impiden aplicar una tarifa fija universal.

Fuentes del repo: `docs/UPGRADE-26924-PLAN.md`, `docs/COMPATIBILITY.md`,
`docs/PER-REQUEST-SPENDING.md`, `docs/THREAD-SPENDING.md`, `CONTRIBUTING.md`,
`cmd/windows-launcher/config_windows.go`, `scripts/install_windows.ps1` y
`scripts/windows_renderer_26924.py`.

Las decisiones de producto están cerradas para esta especificación. M2 aún debe
cualificar campos de parentesco y cobertura efectiva de uso en la nueva build;
la documentación pública no demuestra qué devuelve cada endpoint de suscripción.
El tiempo de actualización de cuota y su resolución requieren observación real.
Estos son criterios de aceptación técnica, no suposiciones de funcionamiento.

## 13. Implementación de la primera entrega DEV

La rama incorpora captura de Responses SSE/JSON y compactación, ledger SQLite
privado, relaciones de turnos/agentes persistentes, ventanas independientes,
normalización ×20, observaciones agrupadas, selección manual y estimador. El
driver fijado es `modernc.org/sqlite v1.38.2`, sin DLL SQLite ni CGO en el binario.
Los resultados de inferencia y sus predicciones/tarifas se congelan al terminar.
Una observación sin señal de cuota conserva el baseline y suma el trabajo
posterior antes de entrenar; al cambiar los participantes se retira su selección.
Los intervalos pendientes sobreviven al reinicio. Cambios de plan/reset los
cierran como inválidos, sin borrar las muestras históricas.

La correlación acepta respuestas nativas exactas a `turn/start` y relaciones
cualificadas de agentes. Un indicador `subagent=false` no acredita una raíz.
Los contratos sintéticos cubren repetición, orden alterado y reutilización de
un hilo de agente para un nuevo turno directo del usuario. La cobertura real de
las distintas fuentes todavía requiere el piloto dentro de DEV.

La API inicial agrupa las operaciones de la sección 9 en tres rutas:

- `GET /v1/usage/turn?threadId=...&turnId=...`: detalle de una raíz.
- `GET /v1/usage/status`: estado del ledger y modelos de ambas ventanas.
- `PATCH /v1/usage/calibration`: `rootId`, `included`, `revision` y
  `includeRelated` opcional para seleccionar un grupo conocido completo.

No se ha añadido todavía listado global paginado, exportación/borrado desde la
UI, gráficos ni alertas de deriva. El detalle de un turno se devuelve completo;
el ensayo de carga de 5.000 raíces × 20 operaciones y los objetivos P95 del
plan quedan pendientes. La retención funcional sí se prueba con límites
configurables y mantiene el máximo predeterminado de 5.000 raíces completas,
sin caducidad temporal.

SQLite usa transacciones y journal DELETE con sincronización FULL. Esta primera
versión crea el esquema 1 y rechaza esquemas futuros; una futura migración de
datos deberá implementar y cualificar su backup antes de cambiar el esquema.
El historial guarda la selección actual y las versiones derivadas del modelo;
no expone todavía una bitácora independiente de todos los cambios del toggle.

La UI 26.924 añade los cuatro controles y el detalle por agente/inferencia.
Los canales de instalación separan manifest, homes, perfil, token, puerto,
identidad y acceso directo. La app DEV necesita su propio inicio de sesión.
La evidencia de pruebas e instalación se documenta en la guía DEV; la rama y
el PR permanecen en desarrollo hasta la aceptación visual y de uso real.
