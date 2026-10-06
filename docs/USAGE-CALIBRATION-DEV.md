# Prueba de consumo y calibración en DEV

Rama: `codex/usage-calibration`. Base de la app: Codex Windows 26.924.

## Abrir desarrollo

La instalación de desarrollo usa el acceso directo **Codex Subscription Router
[DEV]** y el ejecutable `%LOCALAPPDATA%\CSR-Dev\app\ChatGPT.exe`.
La actualización compartida conecta PROD y DEV al mismo registro de cuentas y
al mismo historial/proyectos. El home primario es `%USERPROFILE%\.codex`; las
cuentas secundarias conservan sus homes actuales dentro de los datos de PROD.
Los perfiles Electron, puertos, tokens e identidad visual siguen separados.

El ledger y las relaciones de calibración permanecen en
`%LOCALAPPDATA%\CSR-Dev\data`. La anterior carpeta `PrimaryHome` de DEV se
conserva. No se copian credenciales ni bases abiertas para sincronizarlas.

**Activación inicial:** requiere sustituir ambos paquetes después de cerrar
las dos apps. Hasta activar esa pareja, la DEV instalada anteriormente mantiene
su historial aislado. Ver [datos compartidos y activación](SHARED-DESKTOP.md).

## Leer el consumo

Junto a **Worked for …** aparecen cuatro controles: calibración, observado,
estimado y coste API equivalente. El detalle permite inspeccionar operaciones y
agentes, sus tokens de entrada/caché/salida, modelo, tiempos y lecturas de cuota.

Todos los porcentajes usan **cuota equivalente Pro ×20**. Por ejemplo:

| Cuenta utilizada | Lectura original consumida | Consumo mostrado ×20 |
| --- | ---: | ---: |
| Plus ×1 | 2 puntos porcentuales | 0,10 % |
| Pro ×5 | 0,4 puntos porcentuales | 0,10 % |
| Pro ×20 | 0,1 puntos porcentuales | 0,10 % |

Se conservan las lecturas originales antes/después. Las ventanas corta y semanal
se presentan por separado. El coste USD es un equivalente API, no un cargo de la
suscripción. Un valor pendiente o desconocido no significa gasto cero.

## Calibrar

1. Ejecutar un turno y esperar a que lleguen su uso y la lectura final de cuota.
2. Abrir el detalle y comprobar cuenta, tiempos, ventanas y operaciones incluidas.
3. Marcar el control de calibración cuando se conozca todo el consumo que ocurrió
   en ese intervalo. La marca comienza apagada y se puede retirar.
4. Si se solaparon varios turnos de DEV, su observación tiene `*`. Incluir todos los
   turnos conocidos del grupo permite usar la suma como una sola muestra.
5. Comprobar la estimación en peticiones posteriores. Se conserva la predicción
   original de cada petición para poder compararla sin entrenar con su resultado.

El servicio compartido registra en el ledger DEV las peticiones de ambos
frontends, por lo que puede identificar los solapamientos de PROD y DEV. El uso
desde otro dispositivo sigue siendo desconocido: dejar sin marcar ese grupo. Una lectura
redondeada sin cambio mantiene el intervalo pendiente: su trabajo se acumula con
las peticiones siguientes hasta obtener señal. Si cambia el grupo hay que volver
a seleccionarlo. Un reset o datos incompletos también pueden impedir entrenar
aunque la marca esté activada; el detalle indica el motivo.

## Casos de aceptación manual

- Turno sencillo y turno con varios agentes: el total debe contener cada operación
  una vez y el detalle debe permitir localizar a cada agente.
- Dos chats simultáneos en una cuenta: observación compartida con `*`, un solo
  grupo de calibración y una acción para incluir a todos sus miembros.
- Distintas cuentas ×1/×5/×20: mismos cálculos en cuota equivalente ×20 y lecturas
  nativas visibles. No sumar la ventana corta con la semanal.
- Cambiar la marca, cerrar DEV y reabrir: historial y selección persistentes.
- Navegar entre chats y estrechar la ventana: controles asociados al turno correcto,
  accesibles por teclado y sin tapar el botón Worked for.
- Usar PROD y DEV a la vez: cuentas, chats y proyectos compartidos; identidad,
  puertos y perfiles separados. La calibración sólo aparece en DEV.
- Abrir el mismo chat en ambas: sólo la ventana propietaria controla su turno
  activo y recibe sus aprobaciones. La otra puede consultar sus actualizaciones.

Se conservan hasta **5.000 turnos raíz finalizados**, con sus descendientes, sin
límite por antigüedad. Los gráficos y alertas de cambios de consumo son una fase
posterior; los datos originales se guardan para poder analizarlos.

## Evidencia técnica

El [informe de entrega](USAGE-CALIBRATION-EVIDENCE.md) registra las pruebas
automatizadas, el paquete instalado y su arranque. Las pruebas sintéticas no sustituyen la
aceptación del usuario con su sesión real ni certifican la precisión inicial del
estimador: esta depende de las muestras seleccionadas.

## Reconstruir DEV desde esta rama

Los comandos siguientes reconstruyen el canal aislado anterior (schema 2).
Para la pareja compartida usar la preparación schema 3 de
[SHARED-DESKTOP.md](SHARED-DESKTOP.md). No mezclar uno de cada tipo sobre los
mismos datos.

Desde el checkout de `codex/usage-calibration`, con Go, Node, Python y PowerShell
7 disponibles y las dependencias de npm instaladas:

```powershell
pwsh -NoProfile -NonInteractive -File scripts/install_windows.ps1 `
  -InstallChannel Development `
  -Source 'C:\Program Files\WindowsApps\OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0' `
  -SkipDependencyInstall -NoLaunch

pwsh -NoProfile -File scripts/verify_windows_build.ps1 `
  -BuildPath "$env:LOCALAPPDATA\CSR-Dev\app" `
  -StateRoot "$env:LOCALAPPDATA\CSR-Dev\data" -PassThru

node tests/windows/profile-menu-render.cjs "$env:LOCALAPPDATA\CSR-Dev\app\resources\app.asar"
```

Para reemplazar una instalación DEV anterior, cerrar primero esa app y añadir
`-Force` al instalador. Se crea un backup y se conservan los datos. El instalador
comprueba el canal y rechaza cambiar una instalación de producción a desarrollo
en su misma ruta. La versión oficial debe coincidir con el perfil exacto; una
actualización del Store requiere adaptar y cualificar otro perfil.
