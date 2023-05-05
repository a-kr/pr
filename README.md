# pr
Утилита для переключения сессий (проектов) в tmux.

Проекты привязаны к каталогам (обычно внутри домашней директории).

Использование:

```
pr someproject
```

Эта команда:

* найдёт открытую сессию tmux с именем someproject, и если такая есть, переключится на неё;
* если сессии нет, найдёт каталог someproject внутри $HOME, создаст новую сессию tmux с рабочим каталогом ~/someproject и переключится на него;
* при создании сессии будут (опционально) проставлены переменные окружения, указанные в конфиге (``pr -edit``), и в первом окне новой сессии будет запущена команда, указанная там же.

Команду можно запускать как снаружи tmux, так и изнутри.

В конфиге можно указывать алиасы для проектов, чтобы не набирать полное имя или путь к каталогу.

Работает также поиск по префиксу (``pr so`` вместо ``pr someproject``).

``pr -T`` создаст временный каталог в /tmp и переключитсрабочих пространствя на него.

``pr`` без параметров напечатает список открытых сессий. ``pr -a`` выведет также сессии, которые открывались ранее (их список сохраняется в конфиге, редактируемом через ``pr -edit``).

``pr -todo`` откроет текстовый редактор с файлом .todo в корне активного проекта. ``pr -w`` выведет todo для каждого проекта из списка.

В конфиге tmux (``~/.tmux.conf``) можно настроить запуск ``pr`` по горячей клавише:
```
bind P display-popup -E -E "pr --interactive"
```

![pr внутри tmux](img/pr-in-tmux.png)

Сборка:
```
make
```

Нужен Go 1.20+.
