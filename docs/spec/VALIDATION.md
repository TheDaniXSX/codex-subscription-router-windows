# Validación de la especificación de c4eb2ea

Fecha: 5 de octubre de 2026. Esta entrega documenta exclusivamente `c4eb2eae4fc5c59987a901d373f7b58f5015c3a9`; no documenta el estado actual ni afirma que ese commit sea una versión estable demostrada.

## Alcance comprobado

- Identidad del commit, fecha, árbol Git `1a2869e94301b18c0b4848e5bd8e15faae8d820d` y 267 archivos versionados, mediante objetos Git locales.
- Seis análisis especializados revalidados contra ese SHA: routing, cuentas/UI, lifecycle/seguridad, capacidades nativas, procedencia/arquitectura y aceptación. Los informes anteriores del estado actual se apartaron y no se publican como esta especificación.
- 77 contratos funcionales/técnicos, 14 divergencias/límites y 12 criterios de aceptación con IDs únicos.
- Generación de un HTML autocontenido desde la fuente: 30 secciones navegables. Node.js usado: `v22.18.0`; no requiere instalar paquetes.
- Suite del visor: **6/6 PASS**, renderizado semántico, anclas/estados, escapes/enlaces no seguros, búsqueda, interacciones DOM sintéticas y generación autocontenida reproducible.
- 43 enlaces a archivos del SHA contrastados con `git ls-tree` y tres enlaces documentales locales existentes. Segunda generación produce exactamente los mismos hashes.
- Los hashes de los 30 archivos previamente modificados/sin commit permanecen intactos; `git diff --check` no detectó errores de whitespace. No se han corregido ni incluido sus cambios en esta entrega.

Comandos del visor:

```text
node build.mjs
node --test test-portal.cjs
```

## Identidad del resultado

| Archivo | SHA-256 |
| --- | --- |
| SPECIFICATION.md | `3610465ea4981e94d1f473943ef851c734da8a6906bca51d59dfa1361b4e119a` |
| index.html | `eacb8a7e969539523e72440538d2d0e4232444e4032b00088e39fbc4b3d69199` |

El HTML incorpora el hash del Markdown. No se edita a mano; una modificación de la fuente o del estilo requiere regenerarlo y actualizar estas identidades.

## Límites

No se ejecutaron tests del router, login, inferencias, redención de resets, voz o Computer Use. Las cifras 129 Python/45 JS/9 release del documento son evidencia histórica guardada en el commit, no suites repetidas durante esta redacción.

Los tests del visor son automáticos/sintéticos: no constituyen inspección visual manual en todos los navegadores o tamaños de pantalla. Los enlaces técnicos están fijados al SHA y se contrastan con el árbol Git local; no necesitan consultar GitHub durante la generación.

Durante la redacción original de esta especificación no se hizo checkout/reset del repositorio, ni se modificaron implementación, cuentas, datos o instalación. Tampoco se publicó, hizo push o merge en GitHub. La incorporación a `docs/spec/` añade únicamente documentación y su visor. La recuperación posterior del checkout y su adaptación a Codex 26.930 se documentan aparte en [RECOVERY-26930.md](../RECOVERY-26930.md); no cambian la base histórica de esta especificación.
