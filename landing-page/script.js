document.addEventListener('DOMContentLoaded', () => {

    const translations = {
        en: {
            nav_features: 'Features',
            nav_how_it_works: 'How it Works',
            nav_download: 'Download',
            hero_title: 'Create the Universe. Write the Destiny.',
            hero_tagline: 'TaleShift: Stories born from your imagination and <strong>brought to life by AI</strong>.',
            hero_description: 'Here, you are the author and the hero. Describe your world, and the AI will weave a <strong>unique story</strong> for you with unpredictable consequences. <strong>Every decision changes everything</strong>.',
            cta_app_store: 'App Store',
            cta_google_play: 'Google Play',
            how_title: 'From Idea to Living World',
            how_description: 'Describe your concept - setting, hero, atmosphere. The AI generates a <strong>unique story</strong> for you, full of secrets and hidden variables. You control the plot using cards: each decision (swipe left or right) changes the world and leads to an <strong>unpredictable ending</strong>.',
            how_step1_title: '1. Your Idea',
            how_step1_text: 'Describe setting, hero, atmosphere.',
            how_step2_title: '2. AI Magic',
            how_step2_text: '<strong>AI creates</strong> a unique world and plot.',
            how_step3_title: '3. Make Choices',
            how_step3_text: 'Control the story via <strong>card swipes</strong>.',
            how_step4_title: '4. Reap Results',
            how_step4_text: 'Witness <strong>unpredictable consequences</strong>.',
            features_title: 'Why TaleShift?',
            feature1_title: 'Limitless Worlds',
            feature1_text: 'Your imagination is the only limit. From cyberpunk megacities to fantasy kingdoms - <strong>create and explore</strong> universes that never existed before.',
            feature2_title: 'Choices with Depth',
            feature2_text: '<strong>Every decision matters</strong>. It affects hidden parameters, relationships, and can lead to <strong>unexpected consequences</strong> dozens of scenes later.',
            feature3_title: 'Living Stories',
            feature3_text: "Worlds react to your actions. Share your creations, play through others' stories, and watch how the narrative can <strong>break or... look back at you</strong>.",
            testimonials_title: 'What Players Say',
            testimonial1_quote: '"Mind-blowing! I started with a simple idea, and it turned into an epic saga I couldn\'t predict. <strong>Every choice felt impactful</strong>."',
            testimonial1_author: '- Alex_Stormrider',
            testimonial2_quote: '"It\'s like dreaming while awake. The AI weaves such intricate details... Sometimes it feels like <strong>the story *knows* me</strong>."',
            testimonial2_author: '- Seraphina_Reads',
            testimonial3_quote: '"Finally, a game where <strong>my creativity matters</strong>! Sharing my world and seeing others explore it is incredibly rewarding."',
            testimonial3_author: '- GlitchWizard',
            audience_title: 'Is TaleShift For You?',
            audience_text: 'TaleShift is perfect for <strong>creative minds</strong>, lovers of <strong>interactive fiction and RPGs</strong>, writers looking for inspiration, and anyone seeking a truly <strong>unique narrative experience</strong> where their choices shape the universe.',
            audience_item1: 'Interactive Story Fans',
            audience_item2: 'Role-Playing Gamers',
            audience_item3: 'Creative Writers & Worldbuilders',
            audience_item4: 'Players Seeking Unique Experiences',
            gallery_title: 'Take a Look at TaleShift',
            gallery_placeholder: '(App screenshots gallery will be here)',
            cta_final_title: 'Ready to Start Your Story?',
            cta_final_description: 'Download TaleShift and dive into worlds waiting for their creator.',
            cta_app_store_final: 'App Store',
            cta_google_play_final: 'Google Play',
            footer_copy: '© 2025 TaleShift. All rights reserved.',
            footer_privacy: 'Privacy Policy',
            footer_terms: 'Terms of Use',
            cookie_text: 'We use cookies and analytics tools to improve our site and your experience. By continuing to use the site, you agree to this.',
            cookie_learn_more: 'Learn more in our',
            cookie_privacy_link: 'Privacy Policy',
            cookie_accept: 'Got it!',
        },
        ru: {
            nav_features: 'Особенности',
            nav_how_it_works: 'Как это работает',
            nav_download: 'Скачать',
            hero_title: 'Создай Вселенную. Напиши Судьбу.',
            hero_tagline: 'TaleShift: Истории, рождённые твоим воображением и <strong>оживлённые ИИ</strong>.',
            hero_description: 'Здесь ты — автор и герой. Опиши свой мир, и ИИ сплетёт для тебя <strong>уникальную историю</strong> с непредсказуемыми последствиями. <strong>Каждое решение меняет всё</strong>.',
            cta_app_store: 'App Store',
            cta_google_play: 'Google Play',
            how_title: 'От Идеи до Живого Мира',
            how_description: 'Опиши свою задумку — сеттинг, герой, атмосфера. Искусственный интеллект генерирует для тебя <strong>уникальную историю</strong>, полную тайн и скрытых переменных. Ты управляешь сюжетом с помощью карт: каждое решение (свайп влево или вправо) меняет мир и ведет к <strong>непредсказуемому финалу</strong>.',
            how_step1_title: '1. Твоя Идея',
            how_step1_text: 'Опиши сеттинг, героя, атмосферу.',
            how_step2_title: '2. Магия ИИ',
            how_step2_text: '<strong>ИИ создает</strong> уникальный мир и сюжет.',
            how_step3_title: '3. Делай Выбор',
            how_step3_text: 'Управляй сюжетом через <strong>свайпы карт</strong>.',
            how_step4_title: '4. Пожинай Плоды',
            how_step4_text: 'Наблюдай за <strong>непредсказуемыми последствиями</strong>.',
            features_title: 'Почему TaleShift?',
            feature1_title: 'Безграничные Миры',
            feature1_text: 'Твоя фантазия — единственный предел. От киберпанк-мегаполисов до фэнтезийных королевств — <strong>создавай и исследуй</strong> вселенные, которых ещё не существовало.',
            feature2_title: 'Выборы с Глубиной',
            feature2_text: '<strong>Каждое решение имеет значение</strong>. Оно влияет на скрытые параметры, отношения и может привести к <strong>неожиданным последствиям</strong> спустя десятки сцен.',
            feature3_title: 'Живые Истории',
            feature3_text: 'Миры реагируют на твои действия. Делись творениями, проходи истории других и наблюдай, как повествование может <strong>ломаться или... смотреть на тебя в ответ</strong>.',
            testimonials_title: 'Что Говорят Игроки',
            testimonial1_quote: '"Удивительно! Я начал с простого идеи, и она превратилась в эпическое повествование, которое я не смог предсказать. <strong>Каждый выбор чувствовался важным</strong>."',
            testimonial1_author: '- Alex_Stormrider',
            testimonial2_quote: '"Это как мечтать наяву. ИИ вплетает такие сложные детали... Иногда кажется, что <strong>история *знает* меня</strong>."',
            testimonial2_author: '- Seraphina_Reads',
            testimonial3_quote: '"Наконец-то игра, где <strong>моё творчество имеет значение</strong>! Делиться своим миром и видеть, как другие его исследуют, — это невероятно приятно."',
            testimonial3_author: '- GlitchWizard',
            audience_title: 'Для Тебя TaleShift?',
            audience_text: 'TaleShift идеально подходит для <strong>творческих умов</strong>, любителей <strong>интерактивной литературы и RPG</strong>, писателей в поисках вдохновения и всех, кто ищет по-настоящему <strong>уникальный нарративный опыт</strong>, где их выборы формируют вселенную.',
            audience_item1: 'Любители интерактивных историй',
            audience_item2: 'Игроки в ролевые игры',
            audience_item3: 'Творческие писатели и строители миров',
            audience_item4: 'Игроки, ищущие уникальный опыт',
            gallery_title: 'Взгляни на TaleShift',
            gallery_placeholder: '(Здесь будет галерея скриншотов приложения)',
            cta_final_title: 'Готов Начать Свою Историю?',
            cta_final_description: 'Скачай TaleShift и погрузись в миры, которые ждут своего создателя.',
            cta_app_store_final: 'App Store',
            cta_google_play_final: 'Google Play',
            footer_copy: '© 2025 TaleShift. Все права защищены.',
            footer_privacy: 'Политика Конфиденциальности',
            footer_terms: 'Условия Использования',
            cookie_text: 'Мы используем файлы cookie и инструменты аналитики для улучшения нашего сайта и вашего опыта. Продолжая использовать сайт, вы соглашаетесь с этим.',
            cookie_learn_more: 'Узнайте больше в нашей',
            cookie_privacy_link: 'Политике Конфиденциальности',
            cookie_accept: 'Понятно!',
        }
    };

    const langButtons = document.querySelectorAll('.lang-button');
    const translatableElements = document.querySelectorAll('[data-lang-key]');

    function setLanguage(lang) {
        if (!translations[lang]) {
            console.warn(`Language '${lang}' not supported. Falling back to 'en'.`);
            lang = 'en'; // Fallback to English if lang is invalid
        }

        translatableElements.forEach(element => {
            const key = element.getAttribute('data-lang-key');
            if (translations[lang] && translations[lang][key]) {
                // Возвращаем простой innerHTML, т.к. DOMParser не решил проблему
                element.innerHTML = translations[lang][key]; 
            } else {
                console.warn(`Translation key '${key}' not found for language '${lang}'.`);
            }
        });

        // Update page language attribute
        document.documentElement.lang = lang;

        // Update active button style
        langButtons.forEach(button => {
            if (button.getAttribute('data-lang') === lang) {
                button.classList.add('active');
            } else {
                button.classList.remove('active');
            }
        });

        // Save language preference
        try {
            localStorage.setItem('taleshift_lang', lang);
        } catch (e) {
            console.error('Failed to save language preference to localStorage:', e);
        }
    }

    function getInitialLanguage() {
        try {
            const savedLang = localStorage.getItem('taleshift_lang');
            if (savedLang && translations[savedLang]) {
                return savedLang;
            }
        } catch (e) {
            console.error('Failed to read language preference from localStorage:', e);
        }

        // Try browser language (get primary language code like 'en', 'ru')
        const browserLang = navigator.language ? navigator.language.split('-')[0] : 'en';
        if (translations[browserLang]) {
            return browserLang;
        }

        return 'en'; // Default to English
    }

    // Add event listeners to buttons
    langButtons.forEach(button => {
        button.addEventListener('click', () => {
            const lang = button.getAttribute('data-lang');
            setLanguage(lang);
        });
    });

    // Set initial language on page load
    const initialLang = getInitialLanguage();
    setLanguage(initialLang);

    // --- Cookie Notice Logic ---
    const cookieNotice = document.getElementById('cookie-notice');
    const cookieAcceptBtn = document.getElementById('cookie-accept');
    const cookieConsentKey = 'taleshift_cookie_consent';

    // Check if consent was already given
    try {
        if (!localStorage.getItem(cookieConsentKey) && cookieNotice) {
            // Use setTimeout to allow the page to render first, then slide in
            setTimeout(() => {
                cookieNotice.classList.add('show');
            }, 500); // Small delay before showing
        }
    } catch (e) {
        console.error('Failed to access localStorage for cookie consent:', e);
        // Still show the banner if localStorage fails
        if (cookieNotice) {
             setTimeout(() => {
                cookieNotice.classList.add('show');
            }, 500);
        }
    }

    // Add event listener to accept button
    if (cookieAcceptBtn && cookieNotice) {
        cookieAcceptBtn.addEventListener('click', () => {
            cookieNotice.classList.remove('show');
            // Optionally, add a class to smoothly transition out before setting display none or removing
            // For simplicity, we just hide it via transform now.
            
            // Save consent
            try {
                localStorage.setItem(cookieConsentKey, 'true');
            } catch (e) {
                console.error('Failed to save cookie consent to localStorage:', e);
            }
        });
    }

    // --- Burger menu logic --- 
    const burger = document.querySelector('.burger');
    const nav = document.querySelector('.nav');
    if (burger && nav) {
        burger.addEventListener('click', () => {
            burger.classList.toggle('burger--active');
            nav.classList.toggle('nav--active'); 
            // Блокировка/разблокировка скролла страницы при открытом/закрытом меню
            document.body.style.overflow = nav.classList.contains('nav--active') ? 'hidden' : '';
        });

        // Закрытие меню при клике на ссылку (для одностраничников)
        nav.querySelectorAll('.nav__link').forEach(link => {
            link.addEventListener('click', () => {
                if (nav.classList.contains('nav--active')) {
                    burger.classList.remove('burger--active');
                    nav.classList.remove('nav--active');
                    document.body.style.overflow = '';
                }
            });
        });

        // Закрытие меню при клике вне его области
        document.addEventListener('click', (event) => {
            if (!nav.contains(event.target) && !burger.contains(event.target) && nav.classList.contains('nav--active')) {
                 burger.classList.remove('burger--active');
                 nav.classList.remove('nav--active');
                 document.body.style.overflow = '';
            }
        });
    }

    // --- Smooth Scroll for anchors --- (если нужно)
    document.querySelectorAll('a[href^="#"]').forEach(anchor => {
        anchor.addEventListener('click', function (e) {
            // Проверяем, не является ли ссылка просто заглушкой или ссылкой на другую страницу
            const hrefAttribute = this.getAttribute('href');
            if (hrefAttribute && hrefAttribute !== '#' && hrefAttribute.startsWith('#')) {
                const targetElement = document.querySelector(hrefAttribute);
                if (targetElement) {
                     e.preventDefault();
                     targetElement.scrollIntoView({ behavior: 'smooth' });
                     // Закрываем бургер-меню, если оно открыто и ссылка из него
                     if (nav && nav.classList.contains('nav--active') && nav.contains(this)) {
                         burger.classList.remove('burger--active');
                         nav.classList.remove('nav--active');
                         document.body.style.overflow = '';
                     }
                 }
            }
        });
    });

    // --- Intersection Observer for animations ---
    const animatedElements = document.querySelectorAll('.animate-on-scroll');
    const featureCards = document.querySelectorAll('.feature-card'); // Получаем карточки фич

    const observerOptions = {
        root: null, // viewport
        rootMargin: '0px',
        threshold: 0.1 // Trigger when 10% of the element is visible
    };

    const observerCallback = (entries, observer) => {
        entries.forEach((entry, index) => {
            if (entry.isIntersecting) {
                // Проверяем, является ли элемент карточкой фичи
                if (entry.target.classList.contains('feature-card')) {
                    // Чередуем анимацию для карточек
                    if (index % 2 === 0) {
                        entry.target.classList.add('is-visible-left');
                    } else {
                        entry.target.classList.add('is-visible-right');
                    }
                } else {
                    // Стандартная анимация для остальных элементов
                    entry.target.classList.add('is-visible');
                }
                observer.unobserve(entry.target); // Stop observing once animated
            }
        });
    };

    const observer = new IntersectionObserver(observerCallback, observerOptions);

    // Наблюдаем за всеми элементами с классом .animate-on-scroll
    animatedElements.forEach(el => observer.observe(el));

    // Отдельно наблюдаем за карточками фич, чтобы применить к ним fadeInLeft/Right
    featureCards.forEach(card => observer.observe(card));

    // --- AI Prompt Placeholder Animation ---
    const promptInput = document.getElementById('ai-prompt-input');
    if (promptInput) {
        const hints = [
            "Главный герой - эльф, ищущий древний артефакт...",
            "Добавьте неожиданный поворот: ...оказывается, его лучший друг - предатель.",
            "Уточните детали мира: ...в этом городе магия запрещена.",
            "Опишите внешность злодея: ...у него механическая рука и шрам на лице.",
            "Главный герой — эльф, ищущий древний артефакт, чтобы спасти погибающее королевство.",
            "Ведьма сбежала из башни ордена и хочет узнать, кто она на самом деле.",
            "Кибердетектив расследует исчезновение ИИ, который мог изменить всё человечество.",
            "Призрак мага возвращается в мир живых, чтобы завершить начатое тысячу лет назад.",
            "Изгой с проклятым амулетом ищет способ изменить прошлое, не разрушив настоящее.",
            "Город умирает от холода, и только один человек знает, где спрятано солнце.",
            "Воин-перевертыш скрывает свою сущность, пока древнее зло не пробудилось вновь.",
            "Одинокий шут оказался последним свидетелем великого заговора.",
            "Наёмник заключил сделку с богом, но теперь за ним охотятся все живые.",
            "Юная хранительница портала теряет контроль, и грани миров начинают рушиться.",
            "Писатель просыпается в мире собственной книги и не помнит, что было дальше.",
            "Арестованный демон предлагает сделку следователю: правда в обмен на свободу.",
            "Герой живёт одну и ту же неделю снова и снова — и каждый раз мир рушится иначе.",
            "Машина, считающая себя человеком, должна убедить остальных, что у неё есть душа.",
            "Мир рушится по кругу, и только ты помнишь, что уже был здесь."
        ];

        let currentHintIndex = -1; // Начнем со случайного выбора
        let currentCharIndex = 0;
        let currentPhase = 'idle'; // idle, typing, pausingAfterTyping, deleting, pausingAfterDeleting
        let typingTimer, deletingTimer, pauseTimer, cursorTimer;
        let cursorVisible = true;
        const cursorChar = '█'; // Символ курсора

        const typingSpeed = 80; // ms
        const deletingSpeed = 30; // ms
        const pauseAfterTypingDuration = 2000; // ms
        const pauseAfterDeletingDuration = 1000; // ms
        const cursorBlinkSpeed = 500; // ms

        function clearTimers() {
            clearTimeout(typingTimer);
            clearTimeout(deletingTimer);
            clearTimeout(pauseTimer);
            clearInterval(cursorTimer);
            cursorTimer = null;
        }

        function updatePlaceholder(text, showCursor = false) {
            promptInput.placeholder = text + (showCursor && cursorVisible ? cursorChar : '');
        }

        function startCursorBlink() {
            if (cursorTimer) clearInterval(cursorTimer);
            cursorVisible = true;
            cursorTimer = setInterval(() => {
                cursorVisible = !cursorVisible;
                // Перерисовываем плейсхолдер с текущим текстом и новым состоянием курсора
                const currentText = promptInput.placeholder.endsWith(cursorChar)
                                   ? promptInput.placeholder.slice(0, -1)
                                   : promptInput.placeholder;
                updatePlaceholder(currentText, true);
            }, cursorBlinkSpeed);
        }

        function stopCursorBlink(showFinalCursor = false) {
            clearInterval(cursorTimer);
            cursorTimer = null;
            cursorVisible = showFinalCursor;
            const currentText = promptInput.placeholder.endsWith(cursorChar)
                               ? promptInput.placeholder.slice(0, -1)
                               : promptInput.placeholder;
            updatePlaceholder(currentText, showFinalCursor);
        }

        function typeChar() {
            const fullHint = hints[currentHintIndex];
            if (currentCharIndex < fullHint.length) {
                const textToShow = fullHint.substring(0, currentCharIndex + 1);
                updatePlaceholder(textToShow, true); // Показываем курсор при печати
                currentCharIndex++;
                typingTimer = setTimeout(typeChar, typingSpeed);
            } else {
                // Закончили печатать
                currentPhase = 'pausingAfterTyping';
                stopCursorBlink(true); // Оставляем курсор видимым
                startCursorBlink();    // Начинаем моргать
                pauseTimer = setTimeout(() => {
                    stopCursorBlink(true); // Останавливаем моргание, курсор виден
                    currentPhase = 'deleting';
                    deleteChar();
                }, pauseAfterTypingDuration);
            }
        }

        function deleteChar() {
            const currentPlaceholder = promptInput.placeholder.endsWith(cursorChar)
                                       ? promptInput.placeholder.slice(0, -1)
                                       : promptInput.placeholder;

            if (currentPlaceholder.length > 0) {
                const textToShow = currentPlaceholder.substring(0, currentPlaceholder.length - 1);
                updatePlaceholder(textToShow, true); // Показываем курсор при стирании
                currentCharIndex--; // Хотя индекс символа здесь не так важен, синхронизируем
                deletingTimer = setTimeout(deleteChar, deletingSpeed);
            } else {
                // Закончили стирать
                currentPhase = 'pausingAfterDeleting';
                stopCursorBlink(true); // Оставляем курсор видимым
                startCursorBlink();    // Начинаем моргать
                pauseTimer = setTimeout(() => {
                    stopCursorBlink(false); // Скрываем курсор перед началом печати нового
                    startNextHint();
                }, pauseAfterDeletingDuration);
            }
        }

        function startNextHint() {
            currentHintIndex = (currentHintIndex + 1) % hints.length;
            currentCharIndex = 0;
            updatePlaceholder('', false);
            currentPhase = 'typing';
            stopCursorBlink(true); // Показываем курсор перед началом печати
            typeChar();
        }

        // Начать анимацию, если поле не в фокусе и пустое
        function initPlaceholderAnimation() {
             if (document.activeElement !== promptInput && promptInput.value === '') {
                clearTimers();
                 if (currentHintIndex === -1) {
                     // Первый запуск - выбираем случайный
                     currentHintIndex = Math.floor(Math.random() * hints.length);
                 } else {
                     // Последующие запуски - берем следующий
                     currentHintIndex = (currentHintIndex + 1) % hints.length;
                 }
                 currentCharIndex = 0;
                 updatePlaceholder('', false); // Очищаем на всякий случай
                 currentPhase = 'typing';
                 stopCursorBlink(true); // Показать курсор перед началом печати
                 typeChar();
             }
        }

        // Остановить анимацию
        function stopPlaceholderAnimation() {
             clearTimers();
             stopCursorBlink(false);
             // Не сбрасываем currentHintIndex, чтобы продолжить с него
             if (currentPhase !== 'idle') {
                 // Если была активна анимация, очистим плейсхолдер
                 // Если пользователь что-то ввел, он увидит свой текст, а не плейсхолдер
                 if (promptInput.value === '') {
                     promptInput.placeholder = '';
                 }
             }
             currentPhase = 'idle';
        }

        // Запускаем при загрузке
        initPlaceholderAnimation();
    }

    // --- Scroll to Download button --- //
    const scrollToDownloadButton = document.getElementById('scroll-to-download-btn');
    const downloadSection = document.getElementById('download');

    if (scrollToDownloadButton && downloadSection) {
        scrollToDownloadButton.addEventListener('click', (e) => {
            e.preventDefault(); // На всякий случай, если кнопка внутри формы
            downloadSection.scrollIntoView({ behavior: 'smooth' });
        });
    }

    // --- Cookie Notice --- (УЖЕ объявлено выше, используем существующие)
    // const cookieNotice = document.getElementById('cookie-notice');
    // const acceptCookiesButton = document.getElementById('accept-cookies');
    // const COOKIE_NAME = 'user_accepted_cookies'; // Используем cookieConsentKey объявленный выше

    // Логика показа/скрытия и установки cookie уже есть выше (строки ~183-218)
    // Поэтому этот блок ниже, добавленный ранее, не нужен и будет удален.
    /*
    if (cookieNotice && acceptCookiesButton) {
        // ... (логика проверки и добавления листенера была здесь)
    }
    */

    // --- Language Switcher --- (УЖЕ объявлено выше, используем существующие)
    // const langButtons = document.querySelectorAll('.lang-button');
    // const currentLang = localStorage.getItem('preferredLang') || 'ru'; // Используем initialLang и localStorage.getItem('taleshift_lang')

    // Логика установки активной кнопки и добавления листенеров уже есть выше (строки ~112-180)
    // Поэтому этот блок ниже, добавленный ранее, не нужен и будет удален.
    /*
    function setActiveLangButton(lang) {
        // ...
    }
    setActiveLangButton(currentLang);
    langButtons.forEach(button => {
        // ...
    });
    */

}); 