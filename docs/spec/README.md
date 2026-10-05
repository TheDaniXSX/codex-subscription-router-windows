# Visualizador local de la especificación

Referencia canónica: **`c4eb2eae4fc5c59987a901d373f7b58f5015c3a9`**, soporte Codex 26.924, previo a analítica de costes/calibración y PROD/DEV. No es la especificación de HEAD ni una certificación de estabilidad de la app actual.

Abre [el visor HTML](index.html) o [la fuente Markdown](SPECIFICATION.md). [source-evidence.json](source-evidence.json) identifica el commit/árbol exactos y [VALIDATION.md](VALIDATION.md) distingue las comprobaciones documentales de los tests históricos del router.

`SPECIFICATION.md` es la única fuente de contenido. `index.html` es una vista generada y no debe editarse a mano. El generador usa únicamente módulos incluidos en Node.js; el portal no necesita servidor, paquetes externos, APIs ni acceso a Internet.

## Generar y comprobar

Desde esta carpeta:

```text
node build.mjs
node --test test-portal.cjs
```

También admite rutas explícitas: `node build.mjs [SPECIFICATION.md] [index.html]`. Abre `index.html` directamente en el navegador (`file://`). La lectura completa funciona sin JavaScript. JavaScript añade búsqueda por palabras, filtro por estado, resaltado del índice y tema claro/oscuro/sistema; si el navegador bloquea almacenamiento local, el tema sigue funcionando durante la sesión.

## Convenciones de la fuente

- Un H1 define el título; H2 las áreas del documento; H3 las fichas o subsecciones; H4 sus apartados.
- Admitidos: párrafos, listas ordenadas/no ordenadas anidadas, tablas semánticas, enlaces, énfasis, código en línea y bloques con triple acento grave. Los diagramas ASCII deben usar bloques `text`.
- Los estados se detectan en líneas `Estado: Implementado · Cubierto`, `Verificación: Pendiente`, `Alcance: Local` (también con el nombre en negrita), o en celdas de tablas. Etiquetas disponibles: Implementado, Cubierto, Pendiente, Local, Histórico. Son etiquetas documentales; el generador no deduce aceptación funcional.
- Para evitar etiquetas ambiguas, en las tablas usa estados positivos y explícitos; no escribas `no Implementado` como estado, sino `Pendiente` con la explicación en otra columna.
- No se admite HTML crudo: se muestra como texto. Los enlaces permiten HTTP/HTTPS, anclas y rutas relativas; se rechazan esquemas ejecutables, URLs `data:`, `file:` y rutas de red. Los enlaces no se consultan durante la generación.
- El generador incluye el SHA-256 exacto del Markdown para identificar la revisión visualizada. No añade fecha variable, por lo que una misma fuente y mismos estilos producen el mismo HTML.

La prueba automática cubre renderizado, escapes, enlaces no seguros, búsqueda, restricciones habituales de `file://`, tamaño y reproducibilidad. No sustituye la inspección visual en un navegador real ni valida el contenido técnico del router.
