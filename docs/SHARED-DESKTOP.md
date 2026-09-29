# PROD y DEV con datos compartidos

Decisión del usuario: 2026-09-29. Rama `codex/usage-calibration`, PR #10.
Compatibilidad inicial: Windows, Codex 26.924.2738.0 / ASAR 26.924.22138.

## Datos y comportamiento

| Elemento | Ubicación / propietario |
| --- | --- |
| Proyectos, chats y home primario | Home de PROD existente (`%USERPROFILE%\.codex`) |
| Cuentas, afinidades y enrutamiento | `Codex Subscription Router Data`; un único servicio |
| Credenciales secundarias | Sus homes existentes dentro de los datos PROD |
| Ledger y relaciones de calibración | `CSR-Dev\data`; un único escritor |
| Controles y selección de calibración | Sólo la UI y API de DEV |
| Perfil Electron, token y puerto de cada ventana | Su directorio operativo independiente |
| Antiguo home privado DEV | Conservado íntegro; no se elimina ni importa automáticamente |

Ambas ventanas utilizan un servicio local independiente. Cada desktop mantiene
un proxy stdio con su propio endpoint de control. El servicio es el único que
abre el registro de cuentas y ejecuta los app-servers oficiales. Se inicia al
abrir la primera app y termina tras un minuto sin clientes ni trabajo pendiente.
Cerrar una ventana no termina el trabajo de la otra.

Los IDs RPC se separan por conexión. Una sola inicialización nativa se comparte
entre clientes compatibles. Un turno activo tiene una ventana propietaria;
las aprobaciones y herramientas interactivas de sus agentes van a esa ventana.
Otra ventana puede leerlo, pero no iniciar o interrumpir ese turno. Desconectar
al propietario no concede sus aprobaciones a otra ventana automáticamente.

El estado global de escritorio mantiene su formato oficial, con un adaptador
en ambos paquetes. Escribe cambios por campos bajo bloqueo, recarga cambios
externos y actualiza las ventanas. Los cambios incompatibles sobre el mismo
campo/lista se rechazan y se conservan en `.codex-global-state.json.csr-conflicts`
para revisión; se muestra un aviso. Las listas no se unen de forma automática.
Los payloads del renderer transportan su estado de origen para detectar
escrituras antiguas incluso después de una recarga del proceso principal.

El servicio captura uso de PROD y DEV en el ledger privado DEV. Esto permite
contabilizar solapamientos conocidos entre ambas ventanas. El gasto de otros
equipos sigue siendo desconocido. La selección para calibrar continúa apagada
por defecto. No cambia la unidad equivalente Pro ×20 ni la retención de 5.000
turnos completos.

## Preparación y activación

El modo compartido usa schema 3. Los dos paquetes deben tener el mismo protocolo,
ID de pareja, binarios de router/CLI y rutas compartidas. El arranque rechaza una
pareja que no tenga un registro de activación `committed`.

Preparar cada canal con `scripts/install_windows.ps1` y:

```powershell
-PrepareOnly -PreparedDestination '<candidato nuevo en el mismo volumen>' `
-SharedStateRoot '<datos actuales de PROD>' `
-SharedPrimaryHome "$env:USERPROFILE\.codex" `
-UsageDataRoot "$env:LOCALAPPDATA\CSR-Dev\data" `
-SharedProtocol 1 -ActivationPairId '<identificador de la pareja>' `
-ControlPort <puerto actual del canal> -NoLaunch
```

Usar `-InstallChannel Production` y `Development` respectivamente, sus destinos
y StateRoot actuales, y la fuente oficial exacta. La preparación guarda un
recibo con hashes, conservando el destino final en el manifiesto. No sustituye
las aplicaciones abiertas ni cambia sus tokens. `-DryRun` no conserva paquetes.

Una vez cualificados los candidatos:

```powershell
pwsh -NoProfile -File scripts/activate_shared_pair.ps1 `
  -ProductionCandidate '<candidato PROD>' `
  -DevelopmentCandidate '<candidato DEV>' -ValidateOnly

pwsh -NoProfile -File scripts/activate_shared_pair.ps1 `
  -ProductionCandidate '<candidato PROD>' `
  -DevelopmentCandidate '<candidato DEV>' -WaitForExit -LaunchDevelopment
```

Ejecutar el activador desde un proceso independiente de las apps. Un helper
descendiente del chat actual puede morir con el Job Object al cerrar PROD.
El activador espera al cierre natural; no termina procesos. Revalida paquetes,
tokens y manifiestos, conserva ambas instalaciones antiguas, publica las nuevas
y habilita el arranque sólo cuando ambas están verificadas. Un fallo durante la
publicación restaura ambas. Un cierre abrupto del activador mantiene la barrera
de arranque; el plan, diario y carpetas previas permiten recuperar sin eliminar
datos. Nunca volver a publicar sobre una operación incompleta sin inspeccionarla.

Antes de publicar, con ambos procesos cerrados, guarda una copia privada de
los metadatos de enrutamiento, estado global y ledger DEV (incluidos los sidecars
SQLite que existan). No incluye credenciales ni bases nativas de conversaciones.
Cada copia se comprueba por hash; las originales permanecen en sus rutas.

## Verificación de implementación

- Suite completa `npm run check:windows`: Go tests/vet, 76 pruebas JS,
  165 pruebas Python Windows, 9 de publicación y verificadores del repositorio.
- Revisión posterior del activador: 15 pruebas, incluyendo fallos durante la
  publicación, fallos durante rollback, error después del commit, rutas solapadas
  y copia privada de metadatos/calibración.
- `go test -race` en broker, mux y transporte de control; callback síncrono de
  parada del backend comprobado por separado.
- CLI oficial, homes sintéticos y dos clientes: proyecto/chat vacío compartidos,
  cambios visibles desde ambos, cierre de un cliente sin perder el otro,
  rechazo de capacidades incompatibles y exclusión de una segunda instancia.
  Cero solicitudes de inferencia.
- Adaptador de estado: 18 pruebas JS, 4 Python y 4 contratos ejecutados sobre
  los bundles exactos de 26.924; sin aceptación visual implícita.

Estos resultados acreditan la implementación. El paquete instalado y la
activación posterior tienen su propio manifiesto/recibo y diario local.

## Promoción futura

Se puede conservar el mismo `UsageRoot` al habilitar posteriormente la UI en
producción, evitando migrar el ledger. Para cambiar su ubicación hay que parar
el único servicio, preservar juntos SQLite y `usage-relations.json`, validar la
copia y cambiar el binding en ambos paquetes. Copiar archivos vivos por separado
no garantiza un snapshot consistente. No se incluye todavía una UI de exportación.

## Aceptación pendiente

- Confirmar en las ventanas reales los tres proyectos y el historial esperado.
- Crear/renombrar un proyecto o chat en una ventana y comprobarlo en la otra.
- Cambiar una etiqueta de cuenta y comprobarla en ambos menús.
- Ejecutar turnos distintos simultáneos, revisar el asterisco y sus agentes.
- Cerrar PROD mientras DEV trabaja, y repetir al revés.
- Comprobar aprobaciones, herramientas interactivas y acceso a los controles
  de consumo junto a Worked for sólo desde DEV.

Las pruebas sintéticas y de protocolo se registran en la entrega. No sustituyen
la aceptación visual ni prueban la precisión estadística de la calibración.
