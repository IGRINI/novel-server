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
            placeholder_hints: [
                "The main character is an elf seeking an ancient artifact...",
                "Add a twist: ...it turns out his best friend is a traitor.",
                "Refine world details: ...in this city, magic is forbidden.",
                "Describe the villain's appearance: ...he has a mechanical arm and a facial scar.",
                "A witch escaped the order's tower and wants to find out who she really is.",
                "A cyber detective investigates the disappearance of an AI that could change humanity.",
                "The ghost of a wizard returns to the living world to finish what he started a thousand years ago.",
                "An outcast with a cursed amulet seeks to change the past without destroying the present.",
                "The city is dying from cold, and only one person knows where the sun is hidden.",
                "A shapeshifting warrior hides his nature until an ancient evil awakens again.",
                "A lonely jester turns out to be the last witness to a grand conspiracy.",
                "A mercenary made a deal with a god, but now all living things are hunting him.",
                "A young portal keeper loses control, and the boundaries of worlds begin to crumble.",
                "A writer wakes up in the world of his own book and doesn't remember what happens next.",
                "An arrested demon offers a deal to the investigator: the truth in exchange for freedom.",
                "The hero lives the same week over and over — and each time the world collapses differently.",
                "A machine that believes itself human must convince others it has a soul.",
                "The world collapses in a loop, and only you remember you've been here before."
            ]
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
            placeholder_hints: [
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
            ]
        }
    };

    const langButtons = document.querySelectorAll('.lang-button');
    const translatableElements = document.querySelectorAll('[data-lang-key]');
    const promptInput = document.getElementById('ai-prompt-input');

    let currentLanguageHints = [];
    let currentHintIndex = -1;
    let currentCharIndex = 0;
    let currentPhase = 'idle';
    let typingTimer, deletingTimer, pauseTimer, cursorTimer;
    let cursorVisible = true;
    const cursorChar = '█';
    const typingSpeed = 80;
    const deletingSpeed = 30;
    const pauseAfterTypingDuration = 2000;
    const pauseAfterDeletingDuration = 1000;
    const cursorBlinkSpeed = 500;

    function setLanguage(lang) {
        if (!translations[lang]) {
            console.warn(`Language '${lang}' not supported. Falling back to 'en'.`);
            lang = 'en';
        }

        translatableElements.forEach(element => {
            const key = element.getAttribute('data-lang-key');
            if (translations[lang] && translations[lang][key]) {
                element.innerHTML = translations[lang][key];
            } else {
                if (key !== 'placeholder_hints') {
                    console.warn(`Translation key '${key}' not found for language '${lang}'.`);
                }
            }
        });

        document.documentElement.lang = lang;

        langButtons.forEach(button => {
            button.classList.toggle('active', button.getAttribute('data-lang') === lang);
        });

        try {
            localStorage.setItem('taleshift_lang', lang);
        } catch (e) {
            console.error('Failed to save language preference to localStorage:', e);
        }

        const newHints = translations[lang]?.placeholder_hints || translations.en.placeholder_hints || [];
        if (promptInput && currentLanguageHints !== newHints) {
            currentLanguageHints = newHints;
            stopPlaceholderAnimation();
            currentHintIndex = -1;
            currentCharIndex = 0;
            if (document.activeElement !== promptInput && promptInput.value === '') {
                initPlaceholderAnimation();
            } else {
                promptInput.placeholder = '';
            }
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
        const browserLang = navigator.language ? navigator.language.split('-')[0] : 'en';
        return translations[browserLang] ? browserLang : 'en';
    }

    langButtons.forEach(button => {
        button.addEventListener('click', () => {
            const lang = button.getAttribute('data-lang');
            setLanguage(lang);
        });
    });

    const initialLang = getInitialLanguage();
    setLanguage(initialLang);

    const cookieNotice = document.getElementById('cookie-notice');
    const cookieAcceptBtn = document.getElementById('cookie-accept');
    const cookieConsentKey = 'taleshift_cookie_consent';

    try {
        if (!localStorage.getItem(cookieConsentKey) && cookieNotice) {
            setTimeout(() => cookieNotice.classList.add('show'), 500);
        }
    } catch (e) {
        console.error('Failed to access localStorage for cookie consent:', e);
        if (cookieNotice) setTimeout(() => cookieNotice.classList.add('show'), 500);
    }

    if (cookieAcceptBtn && cookieNotice) {
        cookieAcceptBtn.addEventListener('click', () => {
            cookieNotice.classList.remove('show');
            try {
                localStorage.setItem(cookieConsentKey, 'true');
            } catch (e) {
                console.error('Failed to save cookie consent to localStorage:', e);
            }
        });
    }

    const burger = document.querySelector('.burger');
    const nav = document.querySelector('.nav');
    if (burger && nav) {
        burger.addEventListener('click', () => {
            const isActive = nav.classList.toggle('nav--active');
            burger.classList.toggle('burger--active', isActive);
            document.body.style.overflow = isActive ? 'hidden' : '';
        });

        nav.querySelectorAll('.nav__link').forEach(link => {
            link.addEventListener('click', () => {
                if (nav.classList.contains('nav--active')) {
                    burger.classList.remove('burger--active');
                    nav.classList.remove('nav--active');
                    document.body.style.overflow = '';
                }
            });
        });

        document.addEventListener('click', (event) => {
            if (!nav.contains(event.target) && !burger.contains(event.target) && nav.classList.contains('nav--active')) {
                 burger.classList.remove('burger--active');
                 nav.classList.remove('nav--active');
                 document.body.style.overflow = '';
            }
        });
    }

    document.querySelectorAll('a[href^="#"]').forEach(anchor => {
        anchor.addEventListener('click', function (e) {
            const hrefAttribute = this.getAttribute('href');
            if (hrefAttribute && hrefAttribute !== '#' && hrefAttribute.startsWith('#')) {
                const targetElement = document.querySelector(hrefAttribute);
                if (targetElement) {
                     e.preventDefault();
                     targetElement.scrollIntoView({ behavior: 'smooth' });
                     if (nav && nav.classList.contains('nav--active') && nav.contains(this)) {
                         burger.classList.remove('burger--active');
                         nav.classList.remove('nav--active');
                         document.body.style.overflow = '';
                     }
                 }
            }
        });
    });

    const animatedElements = document.querySelectorAll('.animate-on-scroll');
    const featureCards = document.querySelectorAll('.feature-card');

    const observerOptions = { root: null, rootMargin: '0px', threshold: 0.1 };

    const observerCallback = (entries, observer) => {
        entries.forEach((entry, index) => {
            if (entry.isIntersecting) {
                if (entry.target.classList.contains('feature-card')) {
                    entry.target.classList.add(index % 2 === 0 ? 'is-visible-left' : 'is-visible-right');
                } else {
                    entry.target.classList.add('is-visible');
                }
                observer.unobserve(entry.target);
            }
        });
    };

    const observer = new IntersectionObserver(observerCallback, observerOptions);
    animatedElements.forEach(el => observer.observe(el));
    featureCards.forEach(card => observer.observe(card));

    function clearTimers() {
        clearTimeout(typingTimer);
        clearTimeout(deletingTimer);
        clearTimeout(pauseTimer);
        clearInterval(cursorTimer);
        cursorTimer = null;
    }

    function updatePlaceholder(text, showCursor = false) {
        if (!promptInput) return;
        promptInput.placeholder = text + (showCursor && cursorVisible ? cursorChar : '');
    }

    function startCursorBlink() {
        if (cursorTimer || !promptInput) return;
        cursorVisible = true;
        cursorTimer = setInterval(() => {
            cursorVisible = !cursorVisible;
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
        if (!promptInput) return;
        const currentText = promptInput.placeholder.endsWith(cursorChar)
                           ? promptInput.placeholder.slice(0, -1)
                           : promptInput.placeholder;
        updatePlaceholder(currentText, showFinalCursor);
    }

    function typeChar() {
        if (currentPhase !== 'typing' || !currentLanguageHints || currentLanguageHints.length === 0) return;

        const fullHint = currentLanguageHints[currentHintIndex];
        if (currentCharIndex < fullHint.length) {
            const textToShow = fullHint.substring(0, currentCharIndex + 1);
            updatePlaceholder(textToShow, true);
            currentCharIndex++;
            typingTimer = setTimeout(typeChar, typingSpeed);
        } else {
            currentPhase = 'pausingAfterTyping';
            stopCursorBlink(true);
            startCursorBlink();
            pauseTimer = setTimeout(() => {
                stopCursorBlink(true);
                currentPhase = 'deleting';
                deleteChar();
            }, pauseAfterTypingDuration);
        }
    }

    function deleteChar() {
         if (currentPhase !== 'deleting' || !promptInput) return;
         const currentPlaceholder = promptInput.placeholder.endsWith(cursorChar)
                                    ? promptInput.placeholder.slice(0, -1)
                                    : promptInput.placeholder;

        if (currentPlaceholder.length > 0) {
            const textToShow = currentPlaceholder.substring(0, currentPlaceholder.length - 1);
            updatePlaceholder(textToShow, true);
            currentCharIndex--;
            deletingTimer = setTimeout(deleteChar, deletingSpeed);
        } else {
            currentPhase = 'pausingAfterDeleting';
            stopCursorBlink(true);
            startCursorBlink();
            pauseTimer = setTimeout(() => {
                stopCursorBlink(false);
                startNextHint();
            }, pauseAfterDeletingDuration);
        }
    }

    function startNextHint() {
         if (!currentLanguageHints || currentLanguageHints.length === 0) {
             currentPhase = 'idle';
             updatePlaceholder('', false);
             return;
         }
         currentHintIndex = (currentHintIndex + 1) % currentLanguageHints.length;
         currentCharIndex = 0;
         updatePlaceholder('', false);
         currentPhase = 'typing';
         stopCursorBlink(true);
         typeChar();
    }

    function initPlaceholderAnimation() {
         if (!promptInput || currentLanguageHints.length === 0) return;
         if (document.activeElement !== promptInput && promptInput.value === '') {
             clearTimers();
             if (currentHintIndex === -1) {
                 currentHintIndex = Math.floor(Math.random() * currentLanguageHints.length);
             }
             currentCharIndex = 0;
             updatePlaceholder('', false);
             currentPhase = 'typing';
             stopCursorBlink(true);
             typeChar();
         } else {
             stopPlaceholderAnimation();
         }
    }

    function stopPlaceholderAnimation() {
         clearTimers();
         stopCursorBlink(false);
         if (promptInput && currentPhase !== 'idle' && promptInput.value === '') {
             promptInput.placeholder = '';
         }
         currentPhase = 'idle';
    }

    if (promptInput) {
        initPlaceholderAnimation();

        promptInput.addEventListener('focus', stopPlaceholderAnimation);
        promptInput.addEventListener('blur', () => {
            if (promptInput.value === '') {
                initPlaceholderAnimation();
            }
        });
    }

    const scrollToDownloadButton = document.getElementById('scroll-to-download-btn');
    const downloadSection = document.getElementById('download');

    if (scrollToDownloadButton && downloadSection) {
        scrollToDownloadButton.addEventListener('click', (e) => {
            e.preventDefault();
            downloadSection.scrollIntoView({ behavior: 'smooth' });
        });
    }

    // --- Логика показа/скрытия юридических секций ---
    const legalLinks = document.querySelectorAll('.footer__links a[href^="#"]');
    const legalSections = document.querySelectorAll('.section--legal');

    legalLinks.forEach(link => {
        link.addEventListener('click', (e) => {
            e.preventDefault(); // Отменяем стандартный переход по якорю

            const targetId = link.getAttribute('href'); // Получаем ID целевой секции (e.g., "#privacy-policy")
            const targetSection = document.querySelector(targetId);

            if (!targetSection) return; // Если секция не найдена, ничего не делаем

            // Проверяем, видима ли уже целевая секция
            const isTargetVisible = targetSection.classList.contains('visible');

            // Сначала скрываем ВСЕ юридические секции
            legalSections.forEach(section => {
                section.classList.remove('visible');
            });

            // Если целевая секция НЕ была видима, показываем её
            if (!isTargetVisible) {
                targetSection.classList.add('visible');

                // Плавно прокручиваем к началу показанной секции
                // Небольшая задержка, чтобы дать время CSS-переходу начаться
                setTimeout(() => {
                    targetSection.scrollIntoView({
                        behavior: 'smooth',
                        block: 'start' // Выравниваем по верхнему краю
                    });
                }, 100); // 100 мс задержки (можно настроить)
            }
            // Если целевая секция БЫЛА видима, то после скрытия всех секций (выше) она останется скрытой
            // То есть повторный клик на ту же ссылку скроет секцию.
        });
    });

}); 